package videocaller

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// currently connected users' session descriptions
type connectionsRegister struct {
	sync.RWMutex
	sessionDescriptions map[uuid.UUID]connCreds
}

type connCreds struct {
	conn               *websocket.Conn
	sessionDescription string
	isCaller           bool
}

var connRegister = connectionsRegister{}

// PeerStatus is an enum for Caller and Callee connection status
type PeerStatus string

const (
	unsetPeerStatus PeerStatus = "unset"        // peer is not set
	initPeerStatus             = "initializing" // peer is connected with WS and is setting SDP and ICE
	setPeerStatus              = "set"          // peer connection has been set
)

var callerReady chan bool
var callerDisconnect chan bool
var calleeDisconnect chan bool
var calleePong chan bool

// @todo rename to keep it semantic
// fore callees SDP
var calleeReady chan bool

type wsIceCandidates struct {
	sync.RWMutex
	messages [][]byte
}

var iceCandidatesCaller wsIceCandidates
var iceCandidatesCallee wsIceCandidates

// valid websocket message types
const wsMsgInitCaller = "InitCaller"
const wsMsgCallerSessionDesc = "CallerSessionDesc"
const wsMsgCalleeSessionDesc = "CalleeSessionDesc"
const wsMsgICECandidate = "ICECandidate"
const wsMsgGatherICECandidates = "gatherICECandidates"

var wsMessageTypes = [...]string{wsMsgInitCaller, wsMsgCallerSessionDesc, wsMsgCalleeSessionDesc, wsMsgICECandidate, wsMsgGatherICECandidates}

type wsMessage struct {
	data    []byte // marshalled websocket message data
	msgType int    // websocket message type identifier
}

// wsMsg is used for handling websocket json messages
type wsPayload struct {
	MsgType string `json:"type"`
	Data    string `json:"data"`
}

// set as  signed int in case of negative counter corner cases
type wsCounter struct {
	sync.RWMutex
	wsCount      int
	callerStatus PeerStatus
	calleeStatus PeerStatus
}

var chatroomCounter = wsCounter{}

var maxConn = 2

// isValidIncomingType validates if incoming wsMsg.MsgType has been defined
// and should be accepted
func (w *wsMsg) isValidIncomingType() (isValid bool) {
	for _, msgType := range wsMessageTypes {
		// only server should send the InitCaller part
		if w.MsgType == "InitCaller" {
			return false
		} else if w.MsgType == msgType {
			return true
		}
	}

	return
}

// WSConn is used to serialize WSConn, and help storing sessionDescriptions
type WSConn struct {
	ID                   uuid.UUID
	conn                 *websocket.Conn
	isCaller             bool
	isGatheringRemoteICE bool
	hasRemoteSDP         bool
	hasLocalSDP          bool
}

func init() {
	connRegister.sessionDescriptions = make(map[uuid.UUID]connCreds)
	callerReady = make(chan bool, 1)
	callerDisconnect = make(chan bool, 1)
	calleeDisconnect = make(chan bool, 1)
	calleePong = make(chan bool, 1)

	chatroomCounter.Lock()
	chatroomCounter.callerStatus = unsetPeerStatus
	chatroomCounter.Unlock()
}

func StartServer(addr string) (err error) {

	http.HandleFunc("/", indexPageHandler)
	http.HandleFunc("/websocket", websocketHandler)

	return http.ListenAndServe(addr, nil)
}

func indexPageHandler(w http.ResponseWriter, r *http.Request) {
	logrus.Info("Got accessed")
	if r.URL.Path == "/" {
		temp := template.Must(template.ParseFiles("templates/template.html"))
		data := struct{ Title string }{Title: "Client to client call"}
		err := temp.Execute(w, data)
		if err != nil {
			logrus.Error(err)
			return
		}
	} else {
		http.FileServer(http.Dir("templates")).ServeHTTP(w, r)
	}
}

func pongHandler(pong string) (err error) {
	logrus.Info("pong received")
	calleePong <- true
	return
}

func websocketHandler(w http.ResponseWriter, req *http.Request) {
	chatroomCounter.RLock()
	if chatroomCounter.wsCount >= maxConn {
		chatroomCounter.RUnlock()
		logrus.Warnf("Maximum connections reached: %d", maxConn)
		// return locked status for too many connections
		w.WriteHeader(http.StatusLocked)
		return
	}
	chatroomCounter.RUnlock()

	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		logrus.Error(err)
		return
	}

	defer conn.Close()

	conn.SetPongHandler(pongHandler)

	logrus.Info("Added new WS connection")

	chatroomCounter.Lock()
	chatroomCounter.wsCount++
	chatroomCounter.Unlock()

	// register new user when conn has been upgraded
	newUUID, err := uuid.NewUUID()
	if err != nil {
		logrus.Error(err)
		return
	}

	curWSConn := WSConn{
		ID:   newUUID,
		conn: conn,
	}

	// @todo consider ws connection drop by caller and callee swap to caller status
	// @todo setup goroutine for caller/callee sync
	connRegister.Lock()
	connRegisterLen := len(connRegister.sessionDescriptions)
	connRegister.Unlock()

	chatroomCounter.Lock()
	if connRegisterLen == 0 && chatroomCounter.callerStatus == unsetPeerStatus {
		chatroomCounter.Unlock()
		curWSConn.initCaller()
	} else if connRegisterLen >= 0 && chatroomCounter.callerStatus != unsetPeerStatus {
		chatroomCounter.Unlock()
		// start goroutine in order and lsiten to possible disconnect
		curWSConn.initCallee()
	} else {
		chatroomCounter.Unlock()
		logrus.Errorf("Unknown connection; callerStatus: %s; connRegisterLen: %d", chatroomCounter.callerStatus, connRegisterLen)
		return
	}

	readBuffer := make(chan wsMessage)
	writeBuffer := make(chan wsMessage)
	closeHandler := make(chan bool, 1) // for exiting handler function

	go curWSConn.readLoop(readBuffer, closeHandler)
	go curWSConn.writeLoop(writeBuffer)

	if curWSConn.isCaller {
		curWSConn.initCaller(writeBuffer)
	} else {
		curWSConn.initCallee()
	}
	// curWSConn.messageLoop()

	<-closeHandler

	logrus.Info("exiting handler, isCaller: ", curWSConn.isCaller)
}

func (wsConn *WSConn) readLoop(messageBuffer chan<- wsMessage, closeHandler chan<- bool) {
	for {
		msgType, msg, err := wsConn.conn.ReadMessage()

		if err != nil {
			logrus.Errorf("Error in receive message: ", err)

			if wsConn.isCaller {
				logrus.Warn("caller websocket disconnect")

				// check callerReady channel and reset if still open
				if len(callerReady) > 0 {
					<-callerReady
				}

				var wsCount int
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = unsetPeerStatus

				if chatroomCounter.wsCount > 1 {
					wsCount = chatroomCounter.wsCount
				}
				chatroomCounter.Unlock()

				if wsCount > 0 {
					callerDisconnect <- true
				}

				// fluch ICE candidates on caller disconnect
				iceCandidatesCaller.Lock()
				iceCandidatesCaller.messages = [][]byte{}
				iceCandidatesCaller.Unlock()

			} else {
				logrus.Warn("callee websocket disconnect")

				chatroomCounter.Lock()
				if chatroomCounter.callerStatus != setPeerStatus && chatroomCounter.calleeStatus != unsetPeerStatus {
					// sending calleeDisconnect channel
				} else if chatroomCounter.callerStatus == setPeerStatus {
					callerReady <- true
				} else {
					logrus.Errorf("Undefined caller status %s and calleeStatus %s, going to close connection", chatroomCounter.callerStatus, chatroomCounter.calleeStatus)
				}
				chatroomCounter.calleeStatus = unsetPeerStatus
				chatroomCounter.Unlock()

				iceCandidatesCallee.Lock()
				iceCandidatesCallee.messages = [][]byte{}
				iceCandidatesCallee.Unlock()
			}

			connRegister.Lock()
			delete(connRegister.sessionDescriptions, wsConn.ID)
			connRegister.Unlock()

			chatroomCounter.Lock()
			logrus.Error("Removing wsCount")
			chatroomCounter.wsCount--

			logrus.Errorf("new count %d", chatroomCounter.wsCount)
			chatroomCounter.Unlock()

			closeHandler <- true

			return
		}

		if msgType != websocket.TextMessage {
			logrus.Errorf("Unknown gorilla websocket message type: %d", msgType)
			return
		}

		var clientWSMessage wsMsg

		if err := json.Unmarshal(msg, &clientWSMessage); err != nil {
			logrus.Errorf("Unable to unmarshal incoming message: ", err)
			continue
		}

		if !clientWSMessage.isValidIncomingType() {
			logrus.Error("Undefined websocketMessageType: ", clientWSMessage.MsgType)
			continue
		}

		if !clientWSMessage.isValidIncomingType() {
			logrus.Errorf("Skipping, unexpected incoming messagetype: %s", clientWSMessage.MsgType)
			continue
		}

		// @todo send to appropriate functions/channels
	}
}

func (wsConn *WSConn) writeLoop(messageBuffer <-chan wsMessage) {
	for {
		message := <-messageBuffer
		err := wsConn.conn.WriteMessage(message.msgType, messagePayload)
		if err != nil {
			logrus.Error("Unable to WriteMessage: ", err)
		}
	}
}

// @todo refactor to move to read and write loop
func (wsConn *WSConn) messageLoop() {
	for {
		if wsConn.isGatheringRemoteICE && wsConn.hasLocalSDP {
			callerReady <- true
			wsConn.calleeICEBuffer()
		}

		logrus.Info("Listening for message, isCaller: ", wsConn.isCaller)
		msgType, msg, err := wsConn.conn.ReadMessage()

		// set connected webrtc check

		if err != nil {
			logrus.Error(err)
			// remove connection from valid sessionDescriptions
			if wsConn.isCaller {
				logrus.Warn("caller websocket disconnect")

				// check callerReady channel and reset if still open
				if len(callerReady) > 0 {
					<-callerReady
				}

				var wsCount int
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = unsetPeerStatus

				if chatroomCounter.wsCount > 1 {
					wsCount = chatroomCounter.wsCount
				}
				chatroomCounter.Unlock()

				if wsCount > 0 {
					callerDisconnect <- true
				}

				// fluch ICE candidates on caller disconnect
				iceCandidatesCaller.Lock()
				iceCandidatesCaller.messages = [][]byte{}
				iceCandidatesCaller.Unlock()

			} else {
				logrus.Warn("callee websocket disconnect")

				chatroomCounter.Lock()
				if chatroomCounter.callerStatus != setPeerStatus && chatroomCounter.calleeStatus != unsetPeerStatus {
					// sending calleeDisconnect channel
				} else if chatroomCounter.callerStatus == setPeerStatus {
					callerReady <- true
				} else {
					logrus.Errorf("Undefined caller status %s and calleeStatus %s, going to close connection", chatroomCounter.callerStatus, chatroomCounter.calleeStatus)
				}
				chatroomCounter.calleeStatus = unsetPeerStatus
				chatroomCounter.Unlock()

				iceCandidatesCallee.Lock()
				iceCandidatesCallee.messages = [][]byte{}
				iceCandidatesCallee.Unlock()
			}

			connRegister.Lock()
			delete(connRegister.sessionDescriptions, wsConn.ID)
			connRegister.Unlock()

			chatroomCounter.Lock()
			logrus.Error("Removing wsCount")
			chatroomCounter.wsCount--

			logrus.Errorf("new count %d", chatroomCounter.wsCount)
			chatroomCounter.Unlock()

			return
		}

		if msgType != websocket.TextMessage {
			logrus.Errorf("Unknown gorilla websocket message type: %d", msgType)
			return
		}

		var clientWSMessage wsMsg

		if err := json.Unmarshal(msg, &clientWSMessage); err != nil {
			logrus.Error(err)
			return
		}

		if !clientWSMessage.isValidIncomingType() {
			logrus.Error("Undefined websocketMessageType: ", clientWSMessage.MsgType)
			continue
		}

		connRegister.Lock()
		_, ok := connRegister.sessionDescriptions[wsConn.ID]
		if !ok {
			curConnCred := connCreds{
				sessionDescription: clientWSMessage.Data,
				conn:               wsConn.conn,
				isCaller:           wsConn.isCaller,
			}

			logrus.Info("Adding new sessionDescription, current count: ", len(connRegister.sessionDescriptions))
			connRegister.sessionDescriptions[wsConn.ID] = curConnCred

			if curConnCred.isCaller {
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = setPeerStatus
				chatroomCounter.Unlock()
				wsConn.hasLocalSDP = true
			}
		}
		connRegister.Unlock()

		logrus.Info("Receiving message type: ", clientWSMessage.MsgType, clientWSMessage.Data)
		logrus.Info(len(connRegister.sessionDescriptions))

		if clientWSMessage.MsgType == wsMsgCalleeSessionDesc {
			connRegister.Lock()
			for _, sd := range connRegister.sessionDescriptions {
				if sd.isCaller {
					logrus.Info("Sending session description to caller")
					// send session description to caller
					calleeSessionMessage := wsMsg{
						MsgType: wsMsgCalleeSessionDesc,
						Data:    clientWSMessage.Data,
					}

					calleeSessionJSON, err := json.Marshal(calleeSessionMessage)
					if err != nil {
						logrus.Error(err)
						connRegister.Unlock()
						return
					}

					sd.conn.WriteMessage(websocket.TextMessage, calleeSessionJSON)

					calleeReady <- true
					continue
				}
			}
			connRegister.Unlock()
		} else if clientWSMessage.MsgType == wsMsgICECandidate {
			logrus.Info("Is caller ICE candidate: ", wsConn.isCaller)
			logrus.Info("%s", clientWSMessage.Data)
			if clientWSMessage.Data == "" {
				logrus.Warn("empty data")
			}
			if wsConn.isCaller {
				iceCandidatesCaller.Lock()
				iceCandidatesCaller.messages = append(iceCandidatesCaller.messages, msg)
				iceCandidatesCaller.Unlock()
			} else {
				iceCandidatesCallee.Lock()
				iceCandidatesCallee.messages = append(iceCandidatesCallee.messages, msg)
				iceCandidatesCallee.Unlock()
			}
		} else if clientWSMessage.MsgType == wsMsgGatherICECandidates {
			// prepare to read callee buffer
			wsConn.isGatheringRemoteICE = true
		}
	}
}

func (wsConn *WSConn) initCaller(messageBuffer chan<- wsMessage) {
	logrus.Info("Initializing caller")

	initCallerMessage := wsPayload{
		MsgType: wsMsgInitCaller,
	}

	initCallerJSON, err := json.Marshal(initCallerMessage)
	if err != nil {
		logrus.Error(err)
		return
	}

	messageBuffer <- wsMessage{
		data:    initCallerJSON,
		msgType: websocket.TextMessage,
	}

	chatroomCounter.Lock()
	chatroomCounter.callerStatus = initPeerStatus
	chatroomCounter.Unlock()

	wsConn.isCaller = true

	return
}

func (wsConn *WSConn) initCallee(messageBuffer chan<- wsMessage) {
	logrus.Info("initCallee ready")

	chatroomCounter.Lock()
	chatroomCounter.calleeStatus = initPeerStatus
	chatroomCounter.Unlock()

	select {
	case <-callerReady:
		logrus.Info("callerReady channel received")
	case <-callerDisconnect:
		// @todo promote callee to caller
		logrus.Warn("Caller disconnected")
		logrus.Info("Current calleeDisconnect len: ", len(calleeDisconnect))

		chatroomCounter.Lock()
		chatroomCounter.calleeStatus = unsetPeerStatus
		chatroomCounter.Unlock()

		// @todo currently causes deadlock
		(wsConn).initCaller(messageBuffer)

		return
	}

	logrus.Info("Sending caller info to reciever")

	for id, sd := range connRegister.sessionDescriptions {

		if id != wsConn.ID && sd.isCaller {
			logrus.Info("Found caller ID")
			callerSessionMessage := wsPayload{
				MsgType: wsMsgCallerSessionDesc,
				Data:    sd.sessionDescription,
			}

			callerSessionJSON, err := json.Marshal(callerSessionMessage)
			if err != nil {
				logrus.Error(err)
				return
			}

			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    callerSessionJSON,
			}
		}
	}

	iceCandidatesCaller.Lock()
	for _, message := range iceCandidatesCaller.messages {
		logrus.Info("Sending caller ICE candidate")
		messageBuffer <- wsMessage{
			msgType: websocket.TextMessage,
			data:    message,
		}
	}
	iceCandidatesCaller.Unlock()

	return
}

// @todo refactor (duplicated with iceCandidatesCaller)
func (wsConn *WSConn) calleeICEBuffer() {
	logrus.Info("receiveICEBuffer ready")

	<-calleeReady

	logrus.Info("received callee Session description")
	iceCandidatesCallee.Lock()
	for {
		if len(iceCandidatesCallee.messages) > 0 {
			logrus.Info("Sending callee ICE candidate")
			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    iceCandidatesCallee.messages[0],
			}
		} else {
			break
		}

	}
	iceCandidatesCallee.Unlock()
}
