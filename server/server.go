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
var calleePong chan bool

// Session description messages in case of client disconnects
var calleeSessionDescription []byte
var callerSessionDescription []byte

// session description exchange channels
var calleeSessionDescriptionChan chan []byte
var callerSessionDescriptionChan chan []byte

var iceCandidatesCaller chan []byte
var iceCandidatesCallee chan []byte

// valid websocket message types
const wsMsgInitCaller = "InitCaller"
const wsMsgCallerSessionDesc = "CallerSessionDesc"
const wsMsgCalleeSessionDesc = "CalleeSessionDesc"
const wsMsgICECandidate = "ICECandidate"
const wsMsgUpgradeToCallerStatus = "UpgradeToCaller"

var wsMessageTypes = [...]string{wsMsgInitCaller, wsMsgCallerSessionDesc, wsMsgCalleeSessionDesc, wsMsgICECandidate, wsMsgUpgradeToCallerStatus}

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
	wsCount      uint64
	callerStatus PeerStatus
	calleeStatus PeerStatus
}

var chatroomStats = wsCounter{}

var maxConn uint64 = 2

var readTimeout = 10 * time.Second

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
	ID        uuid.UUID
	conn      *websocket.Conn
	isCaller  bool
	reconnect bool
	mux       sync.RWMutex
}

func init() {
	callerReady = make(chan bool, 1)
	callerDisconnect = make(chan bool, 1)
	calleePong = make(chan bool, 1)

	// peer session description exchange
	calleeSessionDescriptionChan = make(chan []byte, 1)
	callerSessionDescriptionChan = make(chan []byte, 1)

	// ice candidates exchange
	iceCandidatesCallee = make(chan []byte)
	iceCandidatesCaller = make(chan []byte)

	// set caller callee statuses to unset
	chatroomStats.callerStatus = unsetPeerStatus
	chatroomStats.calleeStatus = unsetPeerStatus
}

func StartServer(addr string) (err error) {

	http.HandleFunc("/", indexPageHandler)
	http.HandleFunc("/websocket", websocketHandler)

	return http.ListenAndServe(addr, nil)
}

func indexPageHandler(w http.ResponseWriter, r *http.Request) {
	logrus.Info("User entered index page")
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
	logrus.Info("Pong received")
	calleePong <- true
	return
}

func websocketHandler(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

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

	// register new user when conn has been upgraded
	newUUID, err := uuid.NewUUID()
	if err != nil {
		logrus.Error(err)
		return
	}

	curWSConn := WSConn{
		ID:        newUUID,
		conn:      conn,
		reconnect: true,
	}

	wsLogger := logrus.WithFields(logrus.Fields{"isCaller": curWSConn.isCaller, "uuid": curWSConn.ID})

	chatroomStats.Lock()
	chatroomStats.wsCount++
	chatroomStats.Unlock()

	for curWSConn.reconnect {
		curWSConn.mux.Lock()
		curWSConn.reconnect = false
		curWSConn.mux.Unlock()

		curWSConn.startChannel(ctx, wsLogger)
	}

	wsLogger.Warn("Removing wsCount")
	chatroomStats.Lock()
	chatroomStats.wsCount--

	wsLogger.Warnf("New connection count %d", chatroomStats.wsCount)
	if curWSConn.isCaller {
		wsLogger.Debug("Unsetting caller status")

		chatroomStats.callerStatus = unsetPeerStatus

		if chatroomStats.wsCount > 0 {
			callerDisconnect <- true
		}
	} else {
		wsLogger.Debug("Unsetting callee status")
		chatroomStats.calleeStatus = unsetPeerStatus
	}
	chatroomStats.Unlock()
}

func (curWSConn *WSConn) startChannel(ctx context.Context, log *logrus.Entry) {
	ctx2, cancel := context.WithCancel(context.Background())

	chatroomStats.RLock()
	log.Debug("Called startChannel ", chatroomStats.callerStatus)
	if chatroomStats.wsCount == 1 && chatroomStats.callerStatus == unsetPeerStatus {
		log.Info("Setting to isCaller")
		curWSConn.isCaller = true
	}
	chatroomStats.RUnlock()

	var wg sync.WaitGroup

	readBuffer := make(chan wsMessage, 10)
	defer close(readBuffer)
	writeBuffer := make(chan wsMessage, 10)
	defer close(writeBuffer)
	// for exiting handler
	closedConn := make(chan bool, 1)
	defer close(closedConn)
	closedWConn := make(chan bool, 1)
	defer close(closedWConn)
	closedInit := make(chan bool, 1)
	defer close(closedInit)
	closedICE := make(chan bool, 1)
	defer close(closedICE)

	// channel closure manager
	go func() {
		select {
		case <-closedConn:
		case <-closedWConn:
		case <-closedInit:
		case <-closedICE:
		case <-ctx.Done():
		case <-ctx2.Done():
		}

		cancel()

		// empty ICE nad SessionDescription channels
		log.Info("Emptying ICE and SessionDescription channels")
		curWSConn.mux.RLock()
		if curWSConn.isCaller {
			for len(iceCandidatesCaller) > 0 {
				<-iceCandidatesCaller
			}
			for len(callerSessionDescriptionChan) > 0 {
				<-callerSessionDescriptionChan
			}
		} else {
			for len(iceCandidatesCallee) > 0 {
				<-iceCandidatesCallee
			}
			for len(calleeSessionDescriptionChan) > 0 {
				<-calleeSessionDescriptionChan
			}
		}
		curWSConn.mux.RUnlock()
		log.Info("Emptied ICE and SessionDescription channels")
	}()

	wg.Add(1)
	go curWSConn.writeLoop(ctx2, writeBuffer, closedWConn, log, &wg)
	wg.Add(1)
	go curWSConn.readLoop(ctx2, readBuffer, closedConn, log, &wg)

	if curWSConn.isCaller {
		wg.Add(1)
		go curWSConn.initCaller(ctx2, writeBuffer, closedInit, log, &wg)
		wg.Add(1)
		go curWSConn.calleeICEBuffer(ctx2, writeBuffer, closedICE, log, &wg)
	} else {
		wg.Add(1)
		go curWSConn.initCallee(ctx2, writeBuffer, closedInit, log, &wg)
		wg.Add(1)
		go curWSConn.callerICEBuffer(ctx2, writeBuffer, closedICE, log, &wg)
	}

	wg.Wait()

	log.Info("Exiting handler")
}

func (wsConn *WSConn) readLoop(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan bool, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Debug("In wg defer in readLoop")
		wg.Done()
	}()

	log.Debug("In readLoop")

readLoop:
	for {
		err := wsConn.conn.SetReadDeadline(time.Now().Add(readTimeout))
		if err != nil {
			log.Error("Could not SetReadDeadline: ", err, wsConn.reconnect)
			break
		}

		msgType, msg, err := wsConn.conn.ReadMessage()
		if err != nil {
			wsConn.mux.RLock()
			log.Error("Error in receive message: ", err, wsConn.reconnect)
			wsConn.mux.RUnlock()
			break
		}

		if msgType != websocket.TextMessage {
			log.Errorf("Unknown gorilla websocket message type: %d", msgType)
			return
		}

		var incomingWSMessage wsPayload

		if err = json.Unmarshal(msg, &incomingWSMessage); err != nil {
			log.Error("Unable to unmarshal incoming message: ", err)
			continue
		}

		if !incomingWSMessage.isValidIncomingType() {
			log.Error("Undefined websocketMessageType: ", incomingWSMessage.MsgType)
			continue
		}

		// @todo send to appropriate functions/channels
		switch incomingWSMessage.MsgType {
		case wsMsgUpgradeToCallerStatus:
			log.Info("Upgrading to Caller")
			break readLoop
		case wsMsgCallerSessionDesc, wsMsgCalleeSessionDesc:
			if !wsConn.isCaller && incomingWSMessage.MsgType == wsMsgCallerSessionDesc {
				log.Error("Unexpected SDP, received Caller SDP from Callee")
				return
			} else if wsConn.isCaller && incomingWSMessage.MsgType == wsMsgCalleeSessionDesc {
				log.Error("Unexpected SDP, received Callee SDP from Caller")
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
			log.Info("Received ICE candidate")
			if incomingWSMessage.Data == "" {
				log.Warn("Empty caller ICE candidate data")
			}
			if wsConn.isCaller {
				iceCandidatesCaller <- msg
			} else {
				iceCandidatesCallee <- msg
			}
		}
	}

	closedConn <- true

	<-ctx.Done()
}

func (wsConn *WSConn) writeLoop(ctx context.Context, messageBuffer <-chan wsMessage, closedConn chan bool, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in writeLoop")
		wg.Done()
	}()

	log.Debug("In write loop")

	for {
		select {
		case message := <-messageBuffer:
			err := wsConn.conn.WriteMessage(message.msgType, message.data)
			if err != nil {
				log.Error("Unable to WriteMessage: ", err)

				// consider the error to be a lost connection or corrupted buffer data
				// close handler in case of this
				closedConn <- true
				return
			}
		case <-ctx.Done():
			log.Info("Client disconnected in writeLoop")
			return
		}
	}
}

func (wsConn *WSConn) initCaller(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- bool, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in initCaller")
		wg.Done()
	}()

	log.Info("Initializing Caller")

	initCallerMessage := wsPayload{
		MsgType: wsMsgInitCaller,
	}

	initCallerJSON, err := json.Marshal(initCallerMessage)
	if err != nil {
		log.Error(err)
		return
	}

	select {
	case <-ctx.Done():
		log.Info("Client disconnected in initCaller")
		return
	default:
		log.Info("Sending initCallerJSON")
		messageBuffer <- wsMessage{
			data:    initCallerJSON,
			msgType: websocket.TextMessage,
		}

		chatroomStats.Lock()
		chatroomStats.callerStatus = initPeerStatus
		chatroomStats.Unlock()
		return
	}
}

func (wsConn *WSConn) initCallee(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan bool, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in initCallee")
		wg.Done()
	}()

	log.Info("initializing Callee")

	chatroomStats.Lock()
	chatroomStats.calleeStatus = initPeerStatus
	chatroomStats.Unlock()

	select {
	case callerSD := <-callerSessionDescriptionChan:
		log.Info("Received caller session description")

		messageBuffer <- wsMessage{
			msgType: websocket.TextMessage,
			data:    callerSD,
		}
	case <-callerDisconnect:
		log.Warn("Caller disconnected")

		// @todo promote callee to caller
		chatroomStats.Lock()
		chatroomStats.calleeStatus = unsetPeerStatus
		chatroomStats.Unlock()

		wsConn.mux.Lock()
		wsConn.reconnect = true
		wsConn.mux.Unlock()

		upgradeToCallerMessage := wsPayload{
			MsgType: wsMsgUpgradeToCallerStatus,
		}

		upgradeToCallerJSON, err := json.Marshal(upgradeToCallerMessage)
		if err != nil {
			log.Error(err)
			return
		}

		messageBuffer <- wsMessage{
			msgType: websocket.TextMessage,
			data:    upgradeToCallerJSON,
		}

		return
	case <-ctx.Done():
		return
	}
}

func (wsConn *WSConn) calleeICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn <-chan bool, log *logrus.Entry, wg *sync.WaitGroup) {
	log.Debug("Waiting for callee session description")

	defer func() {
		log.Info("In wg defer in calleeICEBuffer")
		wg.Done()
	}()

	var calleeSD []byte
	select {
	case calleeSD = <-calleeSessionDescriptionChan:
		break
	case <-ctx.Done():
		return
	}

	log.Info("Received callee session description")

	messageBuffer <- wsMessage{
		msgType: websocket.TextMessage,
		data:    calleeSD,
	}

	log.Info("Waiting for callee ice buffer")

	for {
		select {
		case iceCandidate := <-iceCandidatesCallee:
			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    iceCandidate,
			}
		case <-ctx.Done():
			return
		}
	}
}

func (wsConn *WSConn) callerICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn <-chan bool, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in callerICEBuffer")
		wg.Done()
	}()

	log.Info("Gathering caller ICE candidates")

	for {
		select {
		case iceCandidate := <-iceCandidatesCaller:
			messageBuffer <- wsMessage{
				msgType: websocket.TextMessage,
				data:    iceCandidate,
			}
		case <-ctx.Done():
			return
		}
	}
}
