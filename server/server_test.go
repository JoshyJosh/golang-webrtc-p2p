package videocaller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/common/log"
	"github.com/sirupsen/logrus"
)

func testServerTeardown(t *testing.T, ws1, ws2, ws3 *websocket.Conn) {
	t.Log("In testServerTeardown")
	// cannot defer during the test, must do Closes on teardown

	if ws3 != nil {
		ws3.Close()
	}

	if ws2 != nil {
		ws2.Close()
	}

	if ws1 != nil {
		ws1.Close()
	}

	time.Sleep(500 * time.Millisecond)

	return
}

// check that server has no active connections and caller is unset
func checkInitialServerStatus(t *testing.T) {
	chatroomStats.RLock()
	if chatroomStats.wsCount != 0 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 0; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != unsetPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, unsetPeerStatus)
	}

	chatroomStats.RUnlock()

	return
}

// first ws connection is commmon among all subtests
func firstWSConnection(t *testing.T, wsUrl string) (ws websocket.Conn) {
	t.Log("Checking initial status")

	wstmp, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	ws = *wstmp
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 1 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 1; have: %d", chatroomStats.wsCount)
	}

	chatroomStats.RUnlock()

	msgType, msgBody, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
	}

	msg1 := wsPayload{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
	}

	chatroomStats.RLock()
	if chatroomStats.callerStatus != initPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}

	chatroomStats.RUnlock()

	return
}

// TestMaximumConnections tries to connect multiple users to websockets
// more than the maximum should be rejected
func testMaximumConnections(t *testing.T, server *httptest.Server) {
	var ws1, ws2, ws3 *websocket.Conn

	t.Cleanup(func() {
		testServerTeardown(t, ws1, ws2, ws3)
	})

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	t.Run("Initial status", checkInitialServerStatus)

	// first connection
	wstmp := firstWSConnection(t, wsUrl)
	ws1 = &wstmp

	// second connection
	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)

	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 2 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}
	chatroomStats.RUnlock()

	// Third person should not be able to connect to peer to peer call
	ws3, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err == nil {
		t.Fatal("Expected error in third response")
	}

	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("Unexpected third response status: %d; expected %d", resp.StatusCode, http.StatusLocked)
	}

	chatroomStats.RLock()
	defer chatroomStats.RUnlock()
	if chatroomStats.wsCount != 2 {
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}
}

// TestReconnectCaller test that if Caller drops connection,
// a new connection is reinitialized
func testReconnectCaller(t *testing.T, server *httptest.Server) {
	t.Log("Testing reconnect caller")
	var ws1 *websocket.Conn

	t.Cleanup(func() {
		testServerTeardown(t, ws1, nil, nil)
	})

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	t.Run("Initial status", checkInitialServerStatus)

	// first connection
	wstmp := firstWSConnection(t, wsUrl)
	ws1 = &wstmp

	ws1.Close()

	// sleep for server to recognize websocket disconnect
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 0 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected number of active connections, expect 0; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != unsetPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection disconnect: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
	chatroomStats.RUnlock()

	ws1, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	msgType, msgBody, err := ws1.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	chatroomStats.RLock()
	defer chatroomStats.RUnlock()
	if chatroomStats.wsCount != 1 {
		t.Fatalf("Unexpected number of active connections, expect 1; have: %d", chatroomStats.wsCount)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
	}

	msg1 := wsPayload{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
}

// TestReconnectCallee test that if Callee drops connection,
// a new connection is reinitialized
func testReconnectCallee(t *testing.T, server *httptest.Server) {
	var ws1, ws2 *websocket.Conn

	t.Cleanup(func() {
		testServerTeardown(t, ws1, ws2, nil)
	})

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	checkInitialServerStatus(t)

	// first connection
	wstmp := firstWSConnection(t, wsUrl)
	ws1 = &wstmp

	// second connection
	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for websocket upgrade
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 2 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
	chatroomStats.RUnlock()

	ws2.Close()

	// sleep for server to recognize websocket disconnect
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 1 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 1; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
	chatroomStats.RUnlock()

	ws2, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for websocket upgrade
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	defer chatroomStats.RUnlock()
	if chatroomStats.wsCount != 2 {
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
}

// testCalleeUpgradeToCaller should upgrade Callee to Caller in case of Caller disconnect
func testCalleeUpgradeToCaller(t *testing.T, server *httptest.Server) {
	// @todo implement upgrade logic
	var ws1, ws2 *websocket.Conn

	t.Cleanup(func() {
		testServerTeardown(t, ws1, ws2, nil)
	})

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	checkInitialServerStatus(t)

	// first connection
	wstmp := firstWSConnection(t, wsUrl)
	ws1 = &wstmp

	// second connection
	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for websocket upgrade
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 2 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
	chatroomStats.RUnlock()

	time.Sleep(500 * time.Millisecond)
	ws1.Close()

	// sleep for server to recognize websocket disconnect
	time.Sleep(500 * time.Millisecond)

	// upgrade caller
	t.Log("Reading receiver message")
	msgType, msgBody, err := ws2.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
	}

	var incomingWSMessage wsPayload

	if err = json.Unmarshal(msgBody, &incomingWSMessage); err != nil {
		t.Fatal("Unable to unmarshal incoming message: ", err)
	}

	if incomingWSMessage.MsgType != wsMsgUpgradeToCallerStatus {
		t.Fatalf("Unknown response message body: %s; expected: %s", incomingWSMessage, wsMsgInitCaller)
	}

	updateToCallerMsg := wsPayload{
		MsgType: wsMsgUpgradeToCallerStatus,
	}

	updateToCallerJSON, err := json.Marshal(updateToCallerMsg)
	if err != nil {
		log.Error(err)
		return
	}

	if err = ws2.WriteMessage(websocket.TextMessage, updateToCallerJSON); err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	if chatroomStats.wsCount != 1 {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected active connections, expect 1; have: %d", chatroomStats.wsCount)
	}

	if chatroomStats.callerStatus != initPeerStatus {
		defer chatroomStats.RUnlock()
		t.Fatalf("Unexpected callerStatus for first connection: %s; expected %s", chatroomStats.callerStatus, initPeerStatus)
	}
	chatroomStats.RUnlock()

	ws1, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	chatroomStats.RLock()
	defer chatroomStats.RUnlock()
	if chatroomStats.wsCount != 2 {
		t.Fatalf("Unexpected active connections, expect 2; have: %d", chatroomStats.wsCount)
	}

	return
}

func TestUserConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))

	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	logrus.SetLevel(logrus.DebugLevel)

	t.Run("testMaximumConnections", func(t *testing.T) {
		testMaximumConnections(t, server)
	})
	t.Run("testReconnectCaller", func(t *testing.T) {
		testReconnectCaller(t, server)
	})
	t.Run("testReconnectCallee", func(t *testing.T) {
		testReconnectCallee(t, server)
	})
	// @todo implement callee upgrade to caller
	t.Run("testCalleeUpgradeToCaller", func(t *testing.T) {
		testCalleeUpgradeToCaller(t, server)
	})
}
