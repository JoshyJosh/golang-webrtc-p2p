package videocaller

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sync"
	"time"

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

var iceCandidatesCaller chan []byte
var iceCandidatesCallee chan []byte

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

var chatroomStats = wsCounter{}

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

	// peer session description exchange
	calleeSessionDescriptionChan = make(chan []byte, 1)
	callerSessionDescriptionChan = make(chan []byte, 1)

	// ice candidates exchange
	iceCandidatesCallee = make(chan []byte)
	iceCandidatesCaller = make(chan []byte)

	// set caller callee statuses to unset
	chatroomStats.Lock()
	chatroomStats.callerStatus = unsetPeerStatus
	chatroomStats.calleeStatus = unsetPeerStatus
	chatroomStats.Unlock()
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
	ctx := req.Context()

	chatroomStats.RLock()
	if chatroomStats.wsCount >= maxConn {
		chatroomStats.RUnlock()
		logrus.Warnf("Maximum connections reached: %d", maxConn)
		// return locked status for too many connections
		w.WriteHeader(http.StatusLocked)
		return
	}
	chatroomStats.RUnlock()

	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		logrus.Error(err)
		return
	}

	defer conn.Close()

	conn.SetPongHandler(pongHandler)

	logrus.Info("Added new WS connection")

	chatroomStats.Lock()
	chatroomStats.wsCount++
	chatroomStats.Unlock()

	defer func() {
		chatroomStats.Lock()
		logrus.Error("Removing wsCount")
		chatroomStats.wsCount--

		logrus.Errorf("New connection count %d", chatroomStats.wsCount)
		chatroomStats.Unlock()
	}()

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

	chatroomStats.Lock()
	if chatroomStats.wsCount == 1 && chatroomStats.callerStatus == unsetPeerStatus {
		curWSConn.isCaller = true
	}

	chatroomStats.Unlock()

	var wg sync.WaitGroup

	readBuffer := make(chan wsMessage, 10)
	defer close(readBuffer)
	writeBuffer := make(chan wsMessage, 10)
	defer close(writeBuffer)
	// // for exiting handler
	closedConn := make(chan bool, 1)
	defer close(closedConn)

	killSwitch := make(chan bool, 1)

	//killswitch
	go func() {
		<-closedConn
		close(killSwitch)
	}()

	wg.Add(1)
	go curWSConn.writeLoop(ctx, writeBuffer, closedConn, killSwitch, &wg)
	wg.Add(1)
	go curWSConn.readLoop(ctx, readBuffer, closedConn, killSwitch, &wg)

	if curWSConn.isCaller {
		wg.Add(1)
		go curWSConn.initCaller(ctx, writeBuffer, closedConn, killSwitch, &wg)
		wg.Add(1)
		go curWSConn.calleeICEBuffer(ctx, writeBuffer, closedConn, killSwitch, &wg)
	} else {
		// 	// wg.Add(1)
		// 	// go curWSConn.initCallee(ctx, writeBuffer, closedConn, &wg)
		// 	// wg.Add(1)
		// 	// go curWSConn.callerICEBuffer(ctx, writeBuffer, closedConn, &wg)
	}

	// <-closeHandler
	wg.Wait()

	logrus.Infof("exiting handler, isCaller: %t", curWSConn.isCaller)
	if curWSConn.isCaller {
		chatroomStats.Lock()
		chatroomStats.callerStatus = unsetPeerStatus
		chatroomStats.Unlock()
	}
	// curWSConn.gracefulClose()

}

func (wsConn *WSConn) readLoop(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- bool, ks <-chan bool, wg *sync.WaitGroup) {
	defer func() {
		logrus.Info("In wg defer in readLoop")
		// wsConn.conn.Close()
		// closedConn <- true
		wg.Done()
	}()

	logrus.Info("In readLoop")
	exitChan := make(chan bool, 1)

	go func() {
		<-ks
		logrus.Info("Client disconnected in readLoop")
		err := wsConn.conn.SetReadDeadline(time.Now())
		if err != nil {
			logrus.Error("Error in setting exiting deadline: ", err)
		}
		exitChan <- true
	}()
	// 	case <-closedConn:
	// 		logrus.Info("Client disconnected")
	// 		return
	// 	}
	// }()
	go func() {
		for {
			err := wsConn.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			if err != nil {
				logrus.Error("Could not SetReadDeadline: ", err)
				closedConn <- true
				return
			}

			msgType, msg, err := wsConn.conn.ReadMessage()
			if err != nil {
				logrus.Error("Error in receive message: ", err)

				closedConn <- true

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
					// closeHandler <- true
					return
				} else if wsConn.isCaller && incomingWSMessage.MsgType == wsMsgCalleeSessionDesc {
					log.Error("Unexpected SDP, received Callee SDP from Caller")
					// closeHandler <- true
					return
				}

				if wsConn.isCaller {
					chatroomStats.Lock()
					chatroomStats.callerStatus = setPeerStatus
					chatroomStats.Unlock()
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
					iceCandidatesCaller <- msg
				} else {
					iceCandidatesCallee <- msg
				}
			}
		}
	}()
	<-exitChan
}

func (wsConn *WSConn) writeLoop(ctx context.Context, messageBuffer <-chan wsMessage, closedConn chan bool, ks <-chan bool, wg *sync.WaitGroup) {
	defer func() {
		logrus.Info("In wg defer in writeLoop")
		// wsConn.conn.Close()
		wg.Done()
	}()

	logrus.Info("In write loop")

	for {
		select {
		case message := <-messageBuffer:
			err := wsConn.conn.WriteMessage(message.msgType, message.data)
			if err != nil {
				logrus.Error("Unable to WriteMessage: ", err)

				// consider the error to be a lost connection or corrupted buffer data
				// close handler in case of this
				closedConn <- true
				return
			}
		case <-ks:
			logrus.Info("Client disconnected in writeLoop")
			return
			// case <-closedConn:
			// 	logrus.Info("closedConn on writeLoop")
			// 	return
		}
	}
}

func (wsConn *WSConn) initCaller(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- bool, ks <-chan bool, wg *sync.WaitGroup) {
	defer func() {
		logrus.Info("In wg defer in initCaller")
		wg.Done()
	}()

	logrus.Info("Initializing caller")

	initCallerMessage := wsPayload{
		MsgType: wsMsgInitCaller,
	}

	initCallerJSON, err := json.Marshal(initCallerMessage)
	if err != nil {
		logrus.Error(err)
		return
	}

	select {
	case <-ks:
		logrus.Info("Client disconnected in initCaller")
		return
	default:
		messageBuffer <- wsMessage{
			data:    initCallerJSON,
			msgType: websocket.TextMessage,
		}

		chatroomStats.Lock()
		chatroomStats.callerStatus = initPeerStatus
		chatroomStats.Unlock()

		wsConn.isCaller = true
		return
	}
}

func (wsConn *WSConn) initCallee(ctx context.Context, messageBuffer chan<- wsMessage, closedConn <-chan bool, wg *sync.WaitGroup) {
	logrus.Info("initializing Callee")

	defer func() {
		logrus.Info("In wg defer in initCallee")
		wg.Done()
	}()

	chatroomStats.Lock()
	chatroomStats.calleeStatus = initPeerStatus
	chatroomStats.Unlock()

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

		chatroomStats.Lock()
		chatroomStats.calleeStatus = unsetPeerStatus
		chatroomStats.Unlock()

		// go wsConn.initCaller(ctx, messageBuffer, wg)

		return
	case <-closedConn:
		return
	case <-ctx.Done():
		return
	}
}

// gracefulClose closes websocket connection, empties ICE buffers
// and removesconnection from chatroomStats.wsCount
func (wsConn *WSConn) gracefulClose() {
	if wsConn.isCaller {
		logrus.Warn("caller websocket disconnect")

		// check callerReady channel and reset if still open
		if len(callerReady) > 0 {
			<-callerReady
		}

		var wsCount int
		chatroomStats.Lock()
		chatroomStats.callerStatus = unsetPeerStatus

		if chatroomStats.wsCount > 1 {
			wsCount = chatroomStats.wsCount
		}
		chatroomStats.Unlock()

		if wsCount > 0 {
			callerDisconnect <- true
		}

		// flush ICE candidates on caller disconnect
		if len(iceCandidatesCaller) != 0 {
			for len(iceCandidatesCaller) > 0 {
				<-iceCandidatesCaller
			}
		}

		// reset session descriptions on disconnect
		callerSessionDescription = nil
		if len(calleeSessionDescriptionChan) > 0 {
			<-calleeSessionDescriptionChan
		}
	} else {
		logrus.Warn("callee websocket disconnect")

		chatroomStats.Lock()
		if chatroomStats.callerStatus != setPeerStatus && chatroomStats.calleeStatus != unsetPeerStatus {
			// sending calleeDisconnect channel
		} else if chatroomStats.callerStatus == setPeerStatus {
			callerReady <- true
		} else {
			logrus.Errorf("Undefined caller status %s and calleeStatus %s, going to close connection", chatroomStats.callerStatus, chatroomStats.calleeStatus)
		}
		chatroomStats.calleeStatus = unsetPeerStatus
		chatroomStats.Unlock()

		// flush ICE candidates on caller disconnect
		if len(iceCandidatesCallee) != 0 {
			for len(iceCandidatesCallee) > 0 {
				<-iceCandidatesCallee
			}
		}

		// reset session descriptions on disconnect
		callerSessionDescription = nil
		if len(calleeSessionDescriptionChan) > 0 {
			<-calleeSessionDescriptionChan
		}
	}

	chatroomStats.Lock()
	logrus.Error("Removing wsCount")
	chatroomStats.wsCount--

	logrus.Errorf("New connection count %d", chatroomStats.wsCount)
	chatroomStats.Unlock()
}

// @todo refactor (duplicated with iceCandidatesCaller)
func (wsConn *WSConn) calleeICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn <-chan bool, ks <-chan bool, wg *sync.WaitGroup) {
	logrus.Info("Waiting for callee session description")

	defer func() {
		logrus.Info("In wg defer in calleeICEBuffer")
		wg.Done()
	}()

	var calleeSD []byte
	select {
	case calleeSD = <-calleeSessionDescriptionChan:
		break
	case <-ks:
		return
	}

	logrus.Info("Received callee session description")

	messageBuffer <- wsMessage{
		msgType: websocket.TextMessage,
		data:    calleeSD,
	}

	logrus.Info("Waiting for callee ice buffer")

	for {
		select {
		case iceCandidate := <-iceCandidatesCallee:
			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    iceCandidate,
			}
		case <-ks:
			return
		}
	}
}

// @todo refactor (duplicated with iceCandidatesCaller)
func (wsConn *WSConn) callerICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn <-chan bool, wg *sync.WaitGroup) {
	logrus.Info("Gathering caller ICE candidates")

	defer func() {
		logrus.Info("In wg defer in callerICEBuffer")
		wg.Done()
	}()

	for {
		select {
		case iceCandidate := <-iceCandidatesCaller:
			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    iceCandidate,
			}
		case <-ctx.Done():
			return
		case <-closedConn:
			return
		}
	}
}
