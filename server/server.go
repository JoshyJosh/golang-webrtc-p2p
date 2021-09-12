package videocaller

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"
)

// PeerStatus is an enum for Caller and Callee connection status
type PeerStatus string

const (
	unsetPeerStatus PeerStatus = "unset"        // peer is not set
	initPeerStatus  PeerStatus = "initializing" // peer is connected with WS and is setting SDP and ICE
	setPeerStatus   PeerStatus = "set"          // peer connection has been set
)

var callerReady chan bool

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
const wsMsgStartSession = "StartSession"
const wsMsgConnectionClosed = "ConnectionClosed"

type wsMessage struct {
	data    []byte                // marshalled websocket message data
	msgType websocket.MessageType // websocket message type identifier
}

// wsMsg is used for handling websocket json messages
type wsPayload struct {
	MsgType string `json:"type"`
	Data    string `json:"data"`
}

// set as  signed int in case of negative counter corner cases
type wsCounter struct {
	wsCount      uint64
	callerStatus PeerStatus
	calleeStatus PeerStatus
	sync.RWMutex
}

var chatroomStats = wsCounter{}

var maxConn uint64 = 2

var pingPeriod = 2 * time.Second

// isValidIncomingType validates if incoming wsMsg.MsgType has been defined
// and should be accepted
func (w *wsPayload) isValidIncomingType() (isValid bool) {

	switch w.MsgType {
	case wsMsgCallerSessionDesc, wsMsgCalleeSessionDesc, wsMsgICECandidate, wsMsgUpgradeToCallerStatus, wsMsgStartSession, wsMsgConnectionClosed:
		return true
	case wsMsgInitCaller:
		logrus.Warn("Received InitCaller")
		fallthrough
	default:
		logrus.Errorf("Invalid incoming message type: %s", w.MsgType)
		return false
	}
}

// WSConn is used to serialize WSConn, and help storing sessionDescriptions
type WSConn struct {
	ID            uuid.UUID
	conn          *websocket.Conn
	isCaller      bool
	reconnect     bool
	reconnectChan chan struct{}
	logger        *logrus.Entry
	sync.RWMutex
}

type wsConnRoster struct {
	wsConnMap map[uuid.UUID]chan struct{}
	sync.RWMutex
}

var WSConnRoster = wsConnRoster{}

func init() {
	callerReady = make(chan bool, 1)

	// peer session description exchange
	calleeSessionDescriptionChan = make(chan []byte, 10)
	callerSessionDescriptionChan = make(chan []byte, 10)

	// ice candidates exchange
	iceCandidatesCallee = make(chan []byte, 50)
	iceCandidatesCaller = make(chan []byte, 50)

	// set caller callee statuses to unset
	chatroomStats.callerStatus = unsetPeerStatus
	chatroomStats.calleeStatus = unsetPeerStatus

	// @todo utilize roster to communicate between peers that have not established ICE connections
	WSConnRoster.wsConnMap = make(map[uuid.UUID]chan struct{})
}

func StartServer(addr string) (err error) {

	http.HandleFunc("/", indexPageHandler)
	http.HandleFunc("/assets/", assetsHandler)
	http.HandleFunc("/websocket", websocketHandler)

	return http.ListenAndServe(addr, nil)
}

func indexPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	logrus.Info("User entered index page")
	temp := template.Must(template.ParseFiles("templates/template.html"))
	data := struct{ Title string }{Title: "Client to client call"}
	err := temp.Execute(w, data)
	if err != nil {
		logrus.Error(err)
		return
	}
}

func assetsHandler(w http.ResponseWriter, r *http.Request) {
	http.FileServer(http.Dir("templates")).ServeHTTP(w, r)
}

func websocketHandler(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	chatroomStats.RLock()
	if chatroomStats.wsCount >= maxConn {
		chatroomStats.RUnlock()
		logrus.Warnf("Maximum websocket connections reached: %d", maxConn)
		// return locked status for too many connections
		w.WriteHeader(http.StatusLocked)
		return
	}
	chatroomStats.RUnlock()

	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		logrus.Error(err)
		return
	}
	defer conn.Close(websocket.StatusInternalError, "defering disconnect")

	logrus.Info("Added new WS connection")

	// register new user when conn has been upgraded
	newUUID, err := uuid.NewUUID()
	if err != nil {
		logrus.Error(err)
		return
	}

	reconnectChan := make(chan struct{}, 1)

	curWSConn := WSConn{
		ID:            newUUID,
		conn:          conn,
		reconnect:     true,
		reconnectChan: reconnectChan,
	}

	logrus.Info("setting uuid: ", newUUID)
	WSConnRoster.Lock()
	WSConnRoster.wsConnMap[newUUID] = reconnectChan
	WSConnRoster.Unlock()
	// defer close(curWSConn.pingStop)

	wsLogger := logrus.WithFields(logrus.Fields{"isCaller": curWSConn.isCaller, "uuid": curWSConn.ID})
	curWSConn.logger = wsLogger

	chatroomStats.Lock()
	chatroomStats.wsCount++
	chatroomStats.Unlock()

	defer func() {
		wsLogger.Warn("Removing wsCount")
		chatroomStats.Lock()
		chatroomStats.wsCount--

		wsLogger.Warnf("New connection count %d", chatroomStats.wsCount)
		if curWSConn.isCaller {
			wsLogger.Debug("Unsetting caller status")
			curWSConn.Lock()
			curWSConn.isCaller = false
			curWSConn.Unlock()

			if chatroomStats.wsCount > 0 {
				WSConnRoster.Lock()
				for uuid, reconnectChan := range WSConnRoster.wsConnMap {
					if uuid.String() != curWSConn.ID.String() {
						logrus.Debug("sending reconnect to channel")
						reconnectChan <- struct{}{}
					}
				}
				WSConnRoster.Unlock()
			}

			wsLogger.Debug("unset caller status")
		}

		chatroomStats.Unlock()
	}()

	for curWSConn.reconnect {
		curWSConn.Lock()
		curWSConn.reconnect = false
		curWSConn.Unlock()

		curWSConn.startChannel(ctx, wsLogger)

		wsLogger.Warn("end of reconnect loop")
	}
	wsLogger.Warn("exiting websocketHandler")
}

func (curWSConn *WSConn) startChannel(ctx context.Context, log *logrus.Entry) {
	ctx2, cancel := context.WithCancel(ctx)

	chatroomStats.Lock()
	logrus.Debugf("Called startChannel %s %d", chatroomStats.callerStatus, chatroomStats.wsCount)

	if chatroomStats.wsCount == 1 && chatroomStats.callerStatus == unsetPeerStatus {
		curWSConn.Lock()
		logrus.Info("Setting to isCaller")
		curWSConn.isCaller = true
		curWSConn.Unlock()
	}

	curWSConn.RLock()
	// update isCallerField
	// @todo make this implicit
	log.Data["isCaller"] = curWSConn.isCaller
	curWSConn.RUnlock()
	chatroomStats.Unlock()

	var wg sync.WaitGroup

	readBuffer := make(chan wsMessage)
	writeBuffer := make(chan wsMessage, 10)

	// for exiting handler
	closedConn := make(chan struct{}, 1)
	closedWConn := make(chan struct{}, 1)
	// closedInit := make(chan struct{}, 1)
	// closedICE := make(chan struct{}, 1)

	gatheringComplete := make(chan struct{}, 1)

	startSession := make(chan struct{}, 1)
	pingStop := make(chan struct{}, 1)

	// channel closure manager
	go func() {
		defer func() {
			log.Warn("deferring startChannel cancel")
			cancel()
		}()

		select {
		case <-curWSConn.reconnectChan:
			log.Debug("setting reconnect to callee by caller")
			curWSConn.reconnect = true
			// curWSConn.skipReconnectTimeout = true
		case <-closedConn:
		case <-pingStop:
		case <-closedWConn:
			// case <-closedInit:
			// case <-closedICE:
			// case <-ctx.Done():
			// case <-ctx2.Done():
			// case <-callerDisconnect:
		}

		// empty ICE nad SessionDescription channels
		log.Info("Emptying ICE and SessionDescription channels")
		curWSConn.RLock()
		if curWSConn.isCaller {
			// empty ICE candidates channel before unsetting caller
			for len(iceCandidatesCaller) > 0 {
				<-iceCandidatesCaller
			}

			for len(callerSessionDescriptionChan) > 0 {
				<-callerSessionDescriptionChan
			}

			chatroomStats.Lock()
			log.Debug("Unsetting caller status")
			chatroomStats.callerStatus = unsetPeerStatus
			chatroomStats.Unlock()

			log.Infof("Emptied ICE and SessionDescription channels: %d, %d", len(iceCandidatesCaller), len(callerSessionDescriptionChan))
		} else {

			chatroomStats.Lock()
			log.Debug("Unsetting callee status")
			chatroomStats.calleeStatus = unsetPeerStatus
			chatroomStats.Unlock()

			log.Infof("Emptied ICE and SessionDescription channels: %d, %d", len(iceCandidatesCallee), len(calleeSessionDescriptionChan))
		}

		// empty callee ICE candidates channel for this session
		for len(iceCandidatesCallee) > 0 {
			<-iceCandidatesCallee
		}

		// empty caller ICE candidates channel for this session
		for len(calleeSessionDescriptionChan) > 0 {
			<-calleeSessionDescriptionChan
		}

		curWSConn.RUnlock()
	}()

	wg.Add(1)
	go curWSConn.readLoop(ctx2, readBuffer, closedConn, startSession, gatheringComplete, log, &wg)
	wg.Add(1)
	go curWSConn.writeLoop(ctx2, writeBuffer, closedWConn, log, &wg)

	// @todo ping loop has issues with reconnects (frames get written, although the connection is not fully established)
	// consider if this is needed after a WebRTC connection has been established
	// wg.Add(1)
	// go curWSConn.pingLoop(ctx2, pingStop, log, &wg)

	// go func(ctx2 context.Context) {
	// 	select {
	// 	case <-startSession:
	// 		var wg2 sync.WaitGroup
	// 		// @TODO set up timeout context and second peer availability
	// 		// during handshake ws channel becomes unresponsive
	// 		// if handshake fails, reconnect websocket
	// 		// <-startSession
	// 		log.Warn("Starting session in handler")
	// 		if curWSConn.isCaller {
	// 			wg2.Add(1)
	// 			go curWSConn.calleeICEBuffer(ctx2, writeBuffer, closedICE, log, &wg2)
	// 			wg2.Add(1)
	// 			go curWSConn.initCaller(ctx2, writeBuffer, closedInit, log, &wg2)
	// 		} else {
	// 			wg2.Add(1)
	// 			go curWSConn.callerICEBuffer(ctx2, writeBuffer, closedICE, log, &wg2)
	// 			wg2.Add(1)
	// 			go curWSConn.initCallee(ctx2, writeBuffer, closedInit, log, &wg2)
	// 		}
	// 		wg2.Wait()
	// 	case <-ctx2.Done():
	// 		break
	// 	}
	// }(ctx2)

	wg.Wait()

	log.Info("Exiting handler")
}

func (curWSConn *WSConn) pingLoop(ctx context.Context, closedConn chan struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Debug("In wg defer in pingLoop")
		wg.Done()
	}()

	for {

		log.Info("Pinging")
		time.Sleep(pingPeriod)

		if err := curWSConn.conn.Ping(ctx); err != nil {
			// @todo check if websocket has disconnected
			log.Error(errors.Wrap(err, "did not receive pong"))
			// @todo consider if a corner case justified the pingLoop having a closed conn call
			// closedConn <- struct{}{}
			return
		}
	}
}

// websocket read loop
func (wsConn *WSConn) readLoop(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- struct{}, startSession chan<- struct{}, gatheringComplete chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Debug("In wg defer in readLoop")
		wg.Done()
	}()

	log.Debug("In readLoop")

readLoop:
	for {
		msgType, msg, err := wsConn.conn.Read(ctx)
		if err != nil {
			log.Error("Error in receive message: ", err, wsConn.reconnect)
			break
		}

		if msgType != websocket.MessageText {
			log.Errorf("Unknown websocket message type: %d", msgType)
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
		case wsMsgStartSession:
			log.Info("Starting session")
			startSession <- struct{}{}
			continue
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
				callerSessionDescriptionChan <- msg
			} else {
				calleeSessionDescriptionChan <- msg
			}
		case wsMsgICECandidate:
			// log.Info("Received ICE candidate")
			if incomingWSMessage.Data == "" {
				log.Warn("Empty caller ICE candidate data")
			}

			if wsConn.isCaller {
				iceCandidatesCaller <- msg
			} else {
				iceCandidatesCallee <- msg
			}
		case wsMsgConnectionClosed:
			log.Warn("Remote connection closed")

			chatroomStats.Lock()
			if wsConn.isCaller {
				chatroomStats.calleeStatus = unsetPeerStatus
			} else {
				chatroomStats.callerStatus = unsetPeerStatus
			}
			chatroomStats.Unlock()

			break readLoop
		}
	}
	log.Debug("Exiting readloop...")

	closedConn <- struct{}{}

	<-ctx.Done()
}

// websocket write loop
func (wsConn *WSConn) writeLoop(ctx context.Context, messageBuffer <-chan wsMessage, closedConn chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in writeLoop")
		wg.Done()
	}()

	log.Debug("In writeLoop")

	for {
		select {
		case message := <-messageBuffer:
			err := wsConn.conn.Write(ctx, websocket.MessageText, message.data)
			if err != nil {
				log.Error("Unable to WriteMessage: ", err)

				// consider the error to be a lost connection or corrupted buffer data
				// close handler in case of this
				closedConn <- struct{}{}
				return
			}
		case <-ctx.Done():
			log.Info("Client disconnected in writeLoop")
			return
		}
	}
}

func (wsConn *WSConn) initCaller(ctx context.Context, messageBuffer chan<- wsMessage, closedInit chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
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
	// case <-time.After(5 * time.Second):
	// 	closedInit <- struct{}{}
	// 	return
	default:
		log.Info("Sending initCallerJSON")
		messageBuffer <- wsMessage{
			data:    initCallerJSON,
			msgType: websocket.MessageText,
		}

		chatroomStats.Lock()
		chatroomStats.callerStatus = initPeerStatus
		chatroomStats.Unlock()
		return
	}
}

func (wsConn *WSConn) initCallee(ctx context.Context, messageBuffer chan<- wsMessage, closedInit chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
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
			msgType: websocket.MessageText,
			data:    callerSD,
		}
	// case <-time.After(5 * time.Second):
	// 	closedInit <- struct{}{}
	// 	return
	case <-ctx.Done():
		return
	}
}

func (wsConn *WSConn) calleeICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
	log.Debug("Waiting for callee session description")

	defer func() {
		log.Info("In wg defer in calleeICEBuffer")
		wg.Done()
	}()

	var calleeSD []byte
	select {
	case calleeSD = <-calleeSessionDescriptionChan:
		break
	// case <-time.After(5 * time.Second):
	// 	closedConn <- struct{}{}
	// 	return
	case <-ctx.Done():
		return
	}

	log.Info("Received callee session description")

	messageBuffer <- wsMessage{
		msgType: websocket.MessageText,
		data:    calleeSD,
	}

	log.Info("Waiting for callee ice buffer")

	for {
		select {
		case iceCandidate := <-iceCandidatesCallee:
			messageBuffer <- wsMessage{
				msgType: websocket.MessageText,
				data:    iceCandidate,
			}
		// case <-time.After(5 * time.Second):
		// 	closedConn <- struct{}{}
		// 	return
		case <-ctx.Done():
			return
		}
	}
}

func (wsConn *WSConn) callerICEBuffer(ctx context.Context, messageBuffer chan<- wsMessage, closedConn chan<- struct{}, log *logrus.Entry, wg *sync.WaitGroup) {
	defer func() {
		log.Info("In wg defer in callerICEBuffer")
		wg.Done()
	}()

	log.Info("Gathering caller ICE candidates")

	for {
		select {
		case iceCandidate := <-iceCandidatesCaller:
			messageBuffer <- wsMessage{
				msgType: websocket.MessageText,
				data:    iceCandidate,
			}
		// case <-time.After(5 * time.Second):
		// 	closedConn <- struct{}{}
		// 	return
		case <-ctx.Done():
			return
		}
	}
}
