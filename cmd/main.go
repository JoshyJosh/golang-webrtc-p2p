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
	isCaller           bool
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

// isValidIncomingType validates if incoming wsMsg.MsgType has been defined
// and should be accepted
func (w *wsMsg) isValidIncomingType() (isValid bool) {
	for _, msgType := range wsMessageTypes {
		// only server should send the InitCaller part
		if w.MsgType == "InitCaller" {
			return false
		} else if w.MsgType == msgType {
			isValid = true
		}
	}

	return
}

// WSConn is used to serialize WSConn, and help storing sessionDescriptions
type WSConn struct {
	ID   uuid.UUID
	conn *websocket.Conn
}

// valid websocket message types
const wsMsgInitCaller = "InitCaller"
const wsMsgCallerSessionDesc = "CallerSessionDesc"
const wsMsgReceiverSessionDesc = "ReceiverSessionDesc"

var wsMessageTypes = [...]string{wsMsgInitCaller, wsMsgCallerSessionDesc, wsMsgReceiverSessionDesc}

func init() {
	connRegister.sessionDescriptions = make(map[uuid.UUID]connCreds)
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
			for callerStatus != callerSetStatus {
				// wait for caller to get  status
			}

			logrus.Info("Found set caller status")
			// set description for receiver
			for id, sd := range connRegister.sessionDescriptions {

				if id != curWSConn.ID && sd.isCaller {
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
					conn.WriteMessage(websocket.TextMessage, callerSessionJSON)
				}
			}
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

			var initWSMessage wsMsg

			if err := json.Unmarshal(msg, &initWSMessage); err != nil {
				logrus.Error(err)
				return
			}

			if initWSMessage.isValidIncomingType() {
				logrus.Error("Undefined websocketMessageType: ", initWSMessage.MsgType)
				return
			}

			connRegister.Lock()
			_, ok := connRegister.sessionDescriptions[curWSConn.ID]
			if !ok {
				curConnCred := connCreds{
					sessionDescription: initWSMessage.Data,
					conn:               curWSConn.conn,
				}

				// first description to be registered is the caller
				if len(connRegister.sessionDescriptions) == 0 {
					curConnCred.isCaller = true
				}

				logrus.Info("Adding new sessionDescription, current count: ", len(connRegister.sessionDescriptions))
				connRegister.sessionDescriptions[curWSConn.ID] = curConnCred

				callerStatus = callerSetStatus
			}

			// if len(connRegister.sessionDescriptions) == 2 {
			// 	logrus.Info("In if statement")
			// 	for id, sd := range connRegister.sessionDescriptions {

			// 		if id != curWSConn.ID && sd.isCaller {
			// 			logrus.Info("Found caller ID")
			// 			remoteSessionMessage := wsMsg{
			// 				MsgType: wsMsgCallerSessionDesc,
			// 				Data:    sd.sessionDescription,
			// 			}

			// 			remoteSessionJSON, err := json.Marshal(remoteSessionMessage)
			// 			if err != nil {
			// 				logrus.Error(err)
			// 				return
			// 			}
			// 			sd.conn.WriteMessage(websocket.TextMessage, remoteSessionJSON)
			// 		} else {
			// 			logrus.Info("Found receiver ID")
			// 			// remoteSessionMessage := wsMsg{
			// 			// 	MsgType: "SessionDesc",
			// 			// 	Data:    wsMsg.Data,
			// 			// }

			// 			// remoteSessionData, err := json.Marshal(remoteSessionMessage)
			// 			// if err != nil {
			// 			// 	logrus.Error(err)
			// 			// 	return
			// 			// }
			// 			// sd.conn.WriteMessage(websocket.TextMessage, remoteSessionData)
			// 		}
			// 	}
			// }
			connRegister.Unlock()
		}
	})

	return http.ListenAndServe(addr, nil)
}
