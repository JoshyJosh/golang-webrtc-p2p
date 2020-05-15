package videocaller

import (
	"encoding/json"
	"fmt"
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
	isCaller           bool // for debugging @todo check if needed
}

var connRegister = connectionsRegister{}

// wsMsg is used for handling websocket json messages
type wsMsg struct {
	MsgType string `json:"type"`
	Data    string `json:"data"`
}

const callerUnsetStatus = "unset"
const callerInitStatus = "initializing"
const callerSetStatus = "set"

var callerReady chan bool
var callerDisconnect chan bool
var receiverDisconnect chan bool
var receiverPong chan bool

// @todo rename to keep it semantic
// fore receivers SDP
var receiverReady chan bool

type wsIceCandidates struct {
	sync.RWMutex
	messages [][]byte
}

var iceCandidatesCaller wsIceCandidates
var iceCandidatesReceiver wsIceCandidates

// valid websocket message types
const wsMsgInitCaller = "InitCaller"
const wsMsgCallerSessionDesc = "CallerSessionDesc"
const wsMsgReceiverSessionDesc = "ReceiverSessionDesc"
const wsMsgICECandidate = "ICECandidate"

var wsMessageTypes = [...]string{wsMsgInitCaller, wsMsgCallerSessionDesc, wsMsgReceiverSessionDesc, wsMsgICECandidate}

// set as  signed int in case of negative counter corner cases

type wsCounter struct {
	sync.RWMutex
	wsCount        int
	callerStatus   string
	receiverStatus string
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
	ID       uuid.UUID
	conn     *websocket.Conn
	isCaller bool
}

func init() {
	connRegister.sessionDescriptions = make(map[uuid.UUID]connCreds)
	callerReady = make(chan bool, 1)
	callerDisconnect = make(chan bool, 1)
	receiverDisconnect = make(chan bool, 1)
	receiverPong = make(chan bool, 1)

	chatroomCounter.Lock()
	chatroomCounter.callerStatus = callerUnsetStatus
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
	receiverPong <- true
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

	// @todo consider ws connection drop by caller and receiver swap to caller status
	// @todo setup goroutine for caller/receiver sync
	connRegister.Lock()
	connRegisterLen := len(connRegister.sessionDescriptions)
	connRegister.Unlock()

	chatroomCounter.Lock()
	if connRegisterLen == 0 && chatroomCounter.callerStatus == callerUnsetStatus {
		chatroomCounter.Unlock()
		curWSConn.initCaller()
	} else if connRegisterLen >= 0 && chatroomCounter.callerStatus != callerUnsetStatus {
		chatroomCounter.Unlock()
		// start goroutine in order and lsiten to possible disconnect
		curWSConn.initReceiver()
	} else {
		chatroomCounter.Unlock()
		logrus.Errorf("Unknown connection; callerStatus: %s; connRegisterLen: %d", chatroomCounter.callerStatus, connRegisterLen)
		return
	}

	curWSConn.messageLoop()

	logrus.Info("exiting handler, isCaller: ", curWSConn.isCaller)
}

func (wsConn *WSConn) messageLoop() {
	for {
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
				chatroomCounter.callerStatus = callerUnsetStatus

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
				logrus.Warn("receiver websocket disconnect")

				chatroomCounter.Lock()
				if chatroomCounter.callerStatus != callerSetStatus && chatroomCounter.receiverStatus != callerUnsetStatus {
					// sending receiverDisconnect channel
				} else if chatroomCounter.callerStatus == callerSetStatus {
					callerReady <- true
				} else {
					// @todo remove since its just for debugging
					logrus.Warnf("Undefined caller status %s and receiverStatus %s, going to close connection", chatroomCounter.callerStatus, chatroomCounter.receiverStatus)
				}
				chatroomCounter.receiverStatus = callerUnsetStatus
				chatroomCounter.Unlock()

				iceCandidatesReceiver.Lock()
				iceCandidatesReceiver.messages = [][]byte{}
				iceCandidatesReceiver.Unlock()
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
				chatroomCounter.callerStatus = callerSetStatus
				chatroomCounter.Unlock()
				go receiverICEBuffer(wsConn.conn)
				callerReady <- true
			}
		}
		connRegister.Unlock()

		logrus.Info("Receiving message type: ", clientWSMessage.MsgType, clientWSMessage.Data)
		logrus.Info(len(connRegister.sessionDescriptions))

		if clientWSMessage.MsgType == wsMsgReceiverSessionDesc {
			connRegister.Lock()
			for _, sd := range connRegister.sessionDescriptions {
				if sd.isCaller {
					logrus.Info("Sending session description to caller")
					// send session description to caller
					receiverSessionMessage := wsMsg{
						MsgType: wsMsgReceiverSessionDesc,
						Data:    clientWSMessage.Data,
					}

					receiverSessionJSON, err := json.Marshal(receiverSessionMessage)
					if err != nil {
						logrus.Error(err)
						connRegister.Unlock()
						return
					}

					sd.conn.WriteMessage(websocket.TextMessage, receiverSessionJSON)

					receiverReady <- true
				}
			}
			connRegister.Unlock()
		} else if clientWSMessage.MsgType == wsMsgICECandidate {
			// @todo possible refactoring
			logrus.Info("Is caller ICE candidate: ", wsConn.isCaller)
			if wsConn.isCaller {
				iceCandidatesCaller.Lock()
				iceCandidatesCaller.messages = append(iceCandidatesCaller.messages, msg)
				// @todo create and send channel to indicate that ICE candidates have been resent
				iceCandidatesCaller.Unlock()
			} else {
				iceCandidatesReceiver.Lock()
				iceCandidatesReceiver.messages = append(iceCandidatesReceiver.messages, msg)
				iceCandidatesReceiver.Unlock()
			}
		}
	}
}

func (wsConn *WSConn) initCaller() {
	logrus.Info("Initializing caller")

	initCallerMessage := wsMsg{
		MsgType: wsMsgInitCaller,
	}

	initCallerJSON, err := json.Marshal(initCallerMessage)
	if err != nil {
		logrus.Error(err)
		return
	}

	err = wsConn.conn.WriteMessage(websocket.TextMessage, initCallerJSON)
	if err != nil {
		logrus.Error(err)
		return
	}

	chatroomCounter.Lock()
	chatroomCounter.callerStatus = callerInitStatus
	chatroomCounter.Unlock()

	wsConn.isCaller = true

	return
}

func (wsConn *WSConn) initReceiver() {
	logrus.Info("initReceiver ready")

	chatroomCounter.Lock()
	chatroomCounter.receiverStatus = callerInitStatus
	chatroomCounter.Unlock()

	//@todo add switch to drop goroutine in case of caller reconnect
receiverLoop:
	for {
		select {
		case <-callerReady:
			logrus.Info("callerReady channel received")
			break receiverLoop
		case <-callerDisconnect:
			// @todo promote receiver to caller
			logrus.Warn("Caller disconnected")
			logrus.Info("Current receiverDisconnect len: ", len(receiverDisconnect))

			chatroomCounter.Lock()
			chatroomCounter.receiverStatus = callerUnsetStatus
			chatroomCounter.Unlock()

			// (*wsConn).initCaller()
			logrus.Info("Initializing caller")

			initCallerMessage := wsMsg{
				MsgType: wsMsgInitCaller,
			}

			initCallerJSON, err := json.Marshal(initCallerMessage)
			if err != nil {
				logrus.Error(err)
				return
			}

			wsConn.conn.WriteMessage(websocket.TextMessage, initCallerJSON)

			chatroomCounter.Lock()
			chatroomCounter.callerStatus = callerInitStatus
			chatroomCounter.Unlock()

			wsConn.isCaller = true

			return
		default:
			// check receiver disconnect when still waiting on session descriptions
			err := wsConn.conn.WriteMessage(websocket.PingMessage, []byte{})
			if err != nil {
				logrus.Error(err)
				return
			}
			continue receiverLoop
		}
	}

	logrus.Info("Sending caller info to reciever")

	for id, sd := range connRegister.sessionDescriptions {

		if id != wsConn.ID && sd.isCaller {
			logrus.Info("Found caller ID")
			callerSessionMessage := wsMsg{
				MsgType: wsMsgCallerSessionDesc,
				Data:    sd.sessionDescription,
			}

			callerSessionJSON, err := json.Marshal(callerSessionMessage)
			if err != nil {
				logrus.Error(err)
				return
			}

			err = wsConn.conn.WriteMessage(websocket.TextMessage, callerSessionJSON)
			if err != nil {
				logrus.Errorf("Error in sending caller Session Description to receiver: %#v\n", err)
				return
			}
		}
	}

	iceCandidatesCaller.Lock()
	for _, message := range iceCandidatesCaller.messages {
		logrus.Info("Sending caller ICE candidate")
		err := wsConn.conn.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			logrus.Errorf("Error in sending caller ICE candidates to receiver: %#v\n", err)
			iceCandidatesCaller.Unlock()
			return
		}
	}
	iceCandidatesCaller.Unlock()
	return
}

// @todo refactor (duplicated with iceCandidatesCaller)
func receiverICEBuffer(wsConn *websocket.Conn) {
	logrus.Info("receiveICEBuffer ready")

	<-receiverReady

	logrus.Info("received receiver Session description")

	for {
		iceCandidatesReceiver.Lock()
		if len(iceCandidatesReceiver.messages) > 0 {
			logrus.Info("Sending receiver ICE candidate")
			fmt.Println(string(iceCandidatesReceiver.messages[0]))
			wsConn.WriteMessage(websocket.TextMessage, iceCandidatesReceiver.messages[0])
			iceCandidatesReceiver.messages = iceCandidatesReceiver.messages[1:]
		}
		iceCandidatesReceiver.Unlock()
	}
}
