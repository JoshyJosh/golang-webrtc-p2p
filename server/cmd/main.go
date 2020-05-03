package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"sync"
	"text/template"

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

var callerStatus = callerUnsetStatus

var callerReady chan bool

type wsIceCandidates struct {
	sync.RWMutex
	messages [][]byte
}

var iceCandidates wsIceCandidates

// valid websocket message types
const wsMsgInitCaller = "InitCaller"
const wsMsgCallerSessionDesc = "CallerSessionDesc"
const wsMsgReceiverSessionDesc = "ReceiverSessionDesc"
const wsMsgICECandidate = "ICECandidate"

var wsMessageTypes = [...]string{wsMsgInitCaller, wsMsgCallerSessionDesc, wsMsgReceiverSessionDesc, wsMsgICECandidate}

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
}

func main() {
	addr := flag.String("address", ":80", "Address to host the HTTP server on.")
	flag.Parse()

	logrus.Println("Listening on", *addr)
	err := serve(*addr)
	if err != nil {
		logrus.Fatalf("Failed to serve: %v", err)
	}
}

func serve(addr string) (err error) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logrus.Info("Got accessed")
		if r.URL.Path == "/" {
			temp := template.Must(template.ParseFiles("template.html"))
			data := struct{ Title string }{Title: "Client to client call"}
			err = temp.Execute(w, data)
			if err != nil {
				logrus.Error(err)
				return
			}
		} else {
			http.FileServer(http.Dir(".")).ServeHTTP(w, r)
		}
	})
	http.HandleFunc("/websocket", func(w http.ResponseWriter, req *http.Request) {

		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			logrus.Error(err)
			return
		}

		logrus.Info("Added new WS connection")

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

		// @todo consider ws connection drop by caller
		// @todo setup goroutine for caller/receiver sync
		connRegister.Lock()
		connRegisterLen := len(connRegister.sessionDescriptions)
		connRegister.Unlock()

		if connRegisterLen == 0 && callerStatus == callerUnsetStatus {
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
			callerStatus = callerInitStatus
		} else if connRegisterLen >= 0 && callerStatus != callerUnsetStatus {

			go initReceiver(&curWSConn)
		}

		for {
			msgType, msg, err := conn.ReadMessage()

			if err != nil {
				logrus.Error(err)
				// remove connection from valid sessionDescriptions
				connRegister.Lock()
				if connRegister.sessionDescriptions[curWSConn.ID].isCaller {
					callerStatus = callerUnsetStatus
				}
				delete(connRegister.sessionDescriptions, curWSConn.ID)
				connRegister.Unlock()
				return
			}

			if msgType != websocket.TextMessage {
				logrus.Error("Unknown gorilla websocket message type: %d", msgType)
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
				}

				// first description to be registered is the caller
				if len(connRegister.sessionDescriptions) == 0 {
					curConnCred.isCaller = true
				}

				logrus.Info("Adding new sessionDescription, current count: ", len(connRegister.sessionDescriptions))
				connRegister.sessionDescriptions[curWSConn.ID] = curConnCred

				if curConnCred.isCaller {
					curWSConn.isCaller = true
					callerStatus = callerSetStatus
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

						curWSConn.conn.WriteMessage(websocket.TextMessage, receiverSessionJSON)
					}
				}
			} else if clientWSMessage.MsgType == wsMsgICECandidate {
				logrus.Info("Is caller:", curWSConn.isCaller)

				iceCandidates.Lock()
				iceCandidates.messages = append(iceCandidates.messages, msg)
				iceCandidates.Unlock()
			}

			connRegister.Unlock()
		}
	})

	return http.ListenAndServe(addr, nil)
}

func initReceiver(wsConn *WSConn) {
	logrus.Info("Caller ready")

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
		iceCandidates.Lock()
		if len(iceCandidates.messages) > 0 {
			logrus.Info("Sending ICE candidate")
			wsConn.conn.WriteMessage(websocket.TextMessage, iceCandidates.messages[0])
			iceCandidates.messages = iceCandidates.messages[1:]
		}
		iceCandidates.Unlock()
	}
}
