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
	wsCount      int
	callerStatus string
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

	chatroomCounter.Lock()
	chatroomCounter.callerStatus = callerUnsetStatus
	chatroomCounter.Unlock()
}

func StartServer(addr string) (err error) {
	// reset globals where needed
	chatroomCounter.Lock()
	chatroomCounter.wsCount = 0
	chatroomCounter.Unlock()

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

func websocketHandler(w http.ResponseWriter, req *http.Request) {
	chatroomCounter.RLock()
	if chatroomCounter.wsCount >= maxConn {
		logrus.Warnf("Maximum connections reached: %d", maxConn)
		// return locked status for too many connections
		w.WriteHeader(http.StatusLocked)
		chatroomCounter.RUnlock()
		return
	}
	chatroomCounter.RUnlock()

	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		logrus.Error(err)
		return
	}

	defer conn.Close()

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
		logrus.Info("Initializing caller")

		initCallerMessage := wsMsg{
			MsgType: wsMsgInitCaller,
		}

		initCallerJSON, err := json.Marshal(initCallerMessage)
		if err != nil {
			logrus.Error(err)
			return
		}

		conn.WriteMessage(websocket.TextMessage, initCallerJSON)
		chatroomCounter.callerStatus = callerInitStatus
		curWSConn.isCaller = true
	} else if connRegisterLen >= 0 && chatroomCounter.callerStatus != callerUnsetStatus {
		go initReceiver(&curWSConn)
	}
	chatroomCounter.Unlock()

	for {
		msgType, msg, err := conn.ReadMessage()

		if err != nil {
			logrus.Error(err)
			// remove connection from valid sessionDescriptions
			connRegister.Lock()
			if curWSConn.isCaller {
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = callerUnsetStatus
				chatroomCounter.Unlock()
			}
			delete(connRegister.sessionDescriptions, curWSConn.ID)
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
			return
		}

		connRegister.Lock()
		_, ok := connRegister.sessionDescriptions[curWSConn.ID]
		if !ok {
			curConnCred := connCreds{
				sessionDescription: clientWSMessage.Data,
				conn:               curWSConn.conn,
				isCaller:           curWSConn.isCaller,
			}

			logrus.Info("Adding new sessionDescription, current count: ", len(connRegister.sessionDescriptions))
			connRegister.sessionDescriptions[curWSConn.ID] = curConnCred

			if curConnCred.isCaller {
				chatroomCounter.Lock()
				chatroomCounter.callerStatus = callerSetStatus
				chatroomCounter.Unlock()
				go receiverICEBuffer(curWSConn.conn)
				callerReady <- true
			}
		}

		logrus.Info("Receiving message type: ", clientWSMessage.MsgType, clientWSMessage.Data)
		logrus.Info(len(connRegister.sessionDescriptions))

		if clientWSMessage.MsgType == wsMsgReceiverSessionDesc {
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
						return
					}

					sd.conn.WriteMessage(websocket.TextMessage, receiverSessionJSON)

					receiverReady <- true
				}
			}
		} else if clientWSMessage.MsgType == wsMsgICECandidate {
			// @todo possible refactoring
			logrus.Info("Is caller ICE candidate: ", curWSConn.isCaller)
			if curWSConn.isCaller {
				iceCandidatesCaller.Lock()
				iceCandidatesCaller.messages = append(iceCandidatesCaller.messages, msg)
				iceCandidatesCaller.Unlock()
			} else {
				iceCandidatesReceiver.Lock()
				iceCandidatesReceiver.messages = append(iceCandidatesReceiver.messages, msg)
				iceCandidatesReceiver.Unlock()
			}
		}

		connRegister.Unlock()
	}
}

func initReceiver(wsConn *WSConn) {
	logrus.Info("initReceiver ready")

	//@todo add switch to drop goroutine in case of caller reconnect
	<-callerReady

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
			wsConn.conn.WriteMessage(websocket.TextMessage, callerSessionJSON)
		}
	}

	for {
		iceCandidatesCaller.Lock()
		if len(iceCandidatesCaller.messages) > 0 {
			logrus.Info("Sending ICE candidate")
			fmt.Println(string(iceCandidatesCaller.messages[0]))
			wsConn.conn.WriteMessage(websocket.TextMessage, iceCandidatesCaller.messages[0])
			iceCandidatesCaller.messages = iceCandidatesCaller.messages[1:]
		}
		iceCandidatesCaller.Unlock()
	}
}

// @todo refactor (duplicated with iceCandidatesCaller)
func receiverICEBuffer(wsConn *websocket.Conn) {
	logrus.Info("receiveICEBuffer ready")

	<-receiverReady

	logrus.Info("received receiver Session description")

	for {
		iceCandidatesReceiver.Lock()
		if len(iceCandidatesReceiver.messages) > 0 {
			logrus.Info("Sending ICE candidate")
			fmt.Println(string(iceCandidatesReceiver.messages[0]))
			wsConn.WriteMessage(websocket.TextMessage, iceCandidatesReceiver.messages[0])
			iceCandidatesReceiver.messages = iceCandidatesReceiver.messages[1:]
		}
		iceCandidatesReceiver.Unlock()
	}
}
