package main

import (
	"encoding/json"
	"flag"
	"net/http"
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
var sessionDescriptions map[uuid.UUID]string

// WSMsg is used for handling websocket json messages
type WSMsg struct {
	MsgType string `json:"type"`
	Data    string `json:"data"`
}

// WSConn is used to serialize WSConn, and help storing sessionDescriptions
type WSConn struct {
	ID   uuid.UUID
	conn *websocket.Conn
}

func init() {
	sessionDescriptions = make(map[uuid.UUID]string)
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
			data := struct{ Title string }{Title: "Reflection test"}
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

		// register new user when conn has been upgraded
		newUUID, err := uuid.NewUUID()
		if err != nil {
			logrus.Error(err)
			return
		}

		newWSConn := WSConn{
			ID:   newUUID,
			conn: conn,
		}

		for {
			msgType, msg, err := conn.ReadMessage()

			if err != nil {
				logrus.Error(err)
				// remove connection from valid sessionDescriptions
				delete(sessionDescriptions, newWSConn.ID)
				return
			}

			if msgType != websocket.TextMessage {
				logrus.Error("Unknown message type: %d", msgType)
				return
			}

			var wsMsg WSMsg

			if err := json.Unmarshal(msg, &wsMsg); err != nil {
				logrus.Error(err)
				return
			}

			if wsMsg.MsgType != "SessionDesc" {
				logrus.Error(err)
				return
			}

			sessionDescriptions[newWSConn.ID] = wsMsg.Data
		}
	})

	return http.ListenAndServe(addr, nil)
}

//@todo add goroutine for async channel support
