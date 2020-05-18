package videocaller

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/prometheus/common/log"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

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

// Session description messages in case of client disconnects
var calleeSessionDescription []byte
var callerSessionDescription []byte

// session description exchange channels
var calleeSessionDescriptionChan chan []byte
var callerSessionDescriptionChan chan []byte

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
func (w *wsPayload) isValidIncomingType() (isValid bool) {
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
	callerReady = make(chan bool, 1)
	callerDisconnect = make(chan bool, 1)
	calleeDisconnect = make(chan bool, 1)
	calleePong = make(chan bool, 1)

	calleeSessionDescriptionChan = make(chan []byte, 1)
	callerSessionDescriptionChan = make(chan []byte, 1)

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

	chatroomCounter.Lock()
	if chatroomCounter.wsCount == 1 && chatroomCounter.callerStatus == unsetPeerStatus {
		curWSConn.isCaller = true
	}

	chatroomCounter.Unlock()

	readBuffer := make(chan wsMessage)
	writeBuffer := make(chan wsMessage)
	// for exiting handler
	closeHandler := make(chan bool, 1)

	go curWSConn.readLoop(readBuffer, closeHandler)
	go curWSConn.writeLoop(writeBuffer, closeHandler)

	if curWSConn.isCaller {
		go curWSConn.initCaller(writeBuffer)
		go curWSConn.calleeICEBuffer(writeBuffer)
	} else {
		go curWSConn.initCallee(writeBuffer)
	}

	<-closeHandler

	curWSConn.gracefulClose()

	logrus.Infof("exiting handler, isCaller: %t", curWSConn.isCaller)
}

func (wsConn *WSConn) readLoop(messageBuffer chan<- wsMessage, closeHandler chan<- bool) {
	for {
		msgType, msg, err := wsConn.conn.ReadMessage()

		if err != nil {
			logrus.Error("Error in receive message: ", err)

			closeHandler <- true

			return
		}

		if msgType != websocket.TextMessage {
			logrus.Errorf("Unknown gorilla websocket message type: %d", msgType)
			return
		}

		var incomingWSMessage wsPayload

		if err := json.Unmarshal(msg, &incomingWSMessage); err != nil {
			logrus.Error("Unable to unmarshal incoming message: ", err)
			continue
		}

		if !incomingWSMessage.isValidIncomingType() {
			logrus.Error("Undefined websocketMessageType: ", incomingWSMessage.MsgType)
			continue
		}

		// @todo send to appropriate functions/channels
		switch incomingWSMessage.MsgType {
		case wsMsgCallerSessionDesc, wsMsgCalleeSessionDesc:
			if !wsConn.isCaller && incomingWSMessage.MsgType == wsMsgCallerSessionDesc {
				log.Error("Unexpected SDP, received Caller SDP from Callee")
				closeHandler <- true
				return
			} else if wsConn.isCaller && incomingWSMessage.MsgType == wsMsgCalleeSessionDesc {
				log.Error("Unexpected SDP, received Callee SDP from Caller")
				closeHandler <- true
				return
			}

			if wsConn.isCaller {
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = setPeerStatus
				chatroomCounter.Unlock()
				callerReady <- true
				// in case of dropout backup the message
				callerSessionDescription = msg
				callerSessionDescriptionChan <- msg
			} else {
				calleeSessionDescription = msg
				calleeSessionDescriptionChan <- msg
			}
		case wsMsgICECandidate:
			logrus.Info("Is caller ICE candidate: ", wsConn.isCaller)
			if incomingWSMessage.Data == "" {
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
		case wsMsgGatherICECandidates:
			//@todo consider goroutine implementation
			logrus.Info("in wsMsgGatherICECandidates case, should be using channels")
		}
	}
}

func (wsConn *WSConn) writeLoop(messageBuffer <-chan wsMessage, closeHandler chan<- bool) {
	for {
		message := <-messageBuffer
		err := wsConn.conn.WriteMessage(message.msgType, message.data)
		if err != nil {
			logrus.Error("Unable to WriteMessage: ", err)

			// consider the error to be a lost connection or corrupted buffer data
			// close handler in case of this
			closeHandler <- true
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
	case callerSD := <-callerSessionDescriptionChan:
		logrus.Info("Received caller session description")

		messageBuffer <- wsMessage{
			msgType: websocket.TextMessage,
			data:    callerSD,
		}
	case <-callerDisconnect:
		// @todo promote callee to caller
		logrus.Warn("Caller disconnected")

		chatroomCounter.Lock()
		chatroomCounter.calleeStatus = unsetPeerStatus
		chatroomCounter.Unlock()

		wsConn.initCaller(messageBuffer)

		return
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

// gracefulClose closes websocket connection, empties ICE buffers
// and removesconnection from chatroomCounter.wsCount
func (wsConn *WSConn) gracefulClose() {
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

		// reset session descriptions on disconnect
		callerSessionDescription = nil
		if len(calleeSessionDescriptionChan) > 0 {
			<-calleeSessionDescriptionChan
		}
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

		// reset session descriptions on disconnect
		callerSessionDescription = nil
		if len(calleeSessionDescriptionChan) > 0 {
			<-calleeSessionDescriptionChan
		}
	}

	chatroomCounter.Lock()
	logrus.Error("Removing wsCount")
	chatroomCounter.wsCount--

	logrus.Errorf("new count %d", chatroomCounter.wsCount)
	chatroomCounter.Unlock()
}

// @todo refactor (duplicated with iceCandidatesCaller)
func (wsConn *WSConn) calleeICEBuffer(messageBuffer chan<- wsMessage) {
	logrus.Info("waiting for callee session description")
	calleSD := <-calleeSessionDescriptionChan

	logrus.Info("callee session description received")

	messageBuffer <- wsMessage{
		msgType: websocket.TextMessage,
		data:    calleSD,
	}

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
