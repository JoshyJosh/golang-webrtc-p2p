package videocaller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testServerTeardown(t *testing.T, ws1, ws2, ws3 *websocket.Conn) {
	t.Log("in testServerTeardown")
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
	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 0 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected active connections, expect 0; have: %d", chatroomCounter.wsCount)
	}

	if chatroomCounter.callerStatus != unsetPeerStatus {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}

	chatroomCounter.RUnlock()

	return
}

// first ws connection is commmon among all subtests
func firstWSConnection(t testing.T, wsUrl string) (ws websocket.Conn) {
	t.Log("Checking initial status")

	wstmp, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	ws = *wstmp
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 1 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected active connections, expect 1; have: %d", chatroomCounter.wsCount)
	}

	chatroomCounter.RUnlock()

	msgType, msgBody, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("unknown response message type: %d", msgType)
	}

	msg1 := wsPayload{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
	}

	chatroomCounter.RLock()
	if chatroomCounter.callerStatus != initPeerStatus {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}

	chatroomCounter.RUnlock()

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
	t.Run("initial status", checkInitialServerStatus)

	// first connection
	wstmp := firstWSConnection(*t, wsUrl)
	ws1 = &wstmp

	// second connection
	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)

	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 2 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
	}
	chatroomCounter.RUnlock()

	// Third person should not be able to connect to peer to peer call
	ws3, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err == nil {
		t.Fatal("expected error in third response")
	}

	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("unexpected third response status: %d; expected %d", resp.StatusCode, http.StatusLocked)
	}

	chatroomCounter.RLock()
	defer chatroomCounter.RUnlock()
	if chatroomCounter.wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
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
	t.Run("initial status", checkInitialServerStatus)

	// first connection
	wstmp := firstWSConnection(*t, wsUrl)
	ws1 = &wstmp

	ws1.Close()

	// sleep for server to recognize websocket disconnect
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 0 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected number of active connections, expect 0; have: %d", chatroomCounter.wsCount)
	}

	if chatroomCounter.callerStatus != unsetPeerStatus {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected callerStatus for first connection disconnect: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}
	chatroomCounter.RUnlock()

	ws1, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	msgType, msgBody, err := ws1.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	chatroomCounter.RLock()
	defer chatroomCounter.RUnlock()
	if chatroomCounter.wsCount != 1 {
		t.Fatalf("unexpected number of active connections, expect 1; have: %d", chatroomCounter.wsCount)
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

	if chatroomCounter.callerStatus != initPeerStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
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
	wstmp := firstWSConnection(*t, wsUrl)
	ws1 = &wstmp

	// second connection
	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for websocket upgrade
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 2 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
	}

	if chatroomCounter.callerStatus != initPeerStatus {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}
	chatroomCounter.RUnlock()

	ws2.Close()

	// sleep for server to recognize websocket disconnect
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	if chatroomCounter.wsCount != 1 {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected active connections, expect 1; have: %d", chatroomCounter.wsCount)
	}

	if chatroomCounter.callerStatus != initPeerStatus {
		defer chatroomCounter.RUnlock()
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}
	chatroomCounter.RUnlock()

	ws2, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for websocket upgrade
	time.Sleep(500 * time.Millisecond)

	chatroomCounter.RLock()
	defer chatroomCounter.RUnlock()
	if chatroomCounter.wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
	}

	if chatroomCounter.callerStatus != initPeerStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
	}
}

// // testCalleeUpgradeToCaller should upgrade Callee to Caller in case of Caller disconnect
// func testCalleeUpgradeToCaller(t *testing.T, server *httptest.Server) {
// 	// @todo implement upgrade logic
// 	var ws1, ws2 *websocket.Conn

// 	t.Cleanup(func() {
// 		testServerTeardown(t, ws1, ws2, nil)
// 	})

// 	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

// 	// check server status
// 	checkInitialServerStatus(t)

// 	// first connection
// 	wstmp := firstWSConnection(*t, wsUrl)
// 	ws1 = &wstmp

// 	// second connection
// 	ws2, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	if resp.StatusCode != http.StatusSwitchingProtocols {
// 		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
// 	}

// 	// sleep for websocket upgrade
// 	time.Sleep(500 * time.Millisecond)

// 	chatroomCounter.RLock()
// 	if chatroomCounter.wsCount != 2 {
// 		defer chatroomCounter.RUnlock()
// 		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
// 	}

// 	if chatroomCounter.callerStatus != initPeerStatus {
// 		defer chatroomCounter.RUnlock()
// 		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
// 	}
// 	chatroomCounter.RUnlock()

// 	ws1.Close()

// 	// sleep for server to recognize websocket disconnect
// 	time.Sleep(500 * time.Millisecond)

// 	// upgrade caller
// 	t.Log("Reading receiver message")
// 	msgType, msgBody, err := ws2.ReadMessage()
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	if msgType != websocket.TextMessage {
// 		t.Fatalf("unknown response message type: %d", msgType)
// 	}

// 	msg1 := wsPayload{}
// 	if err = json.Unmarshal(msgBody, &msg1); err != nil {
// 		t.Fatal(err)
// 	}

// 	if msg1.MsgType != wsMsgInitCaller {
// 		t.Fatalf("unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
// 	}

// 	chatroomCounter.RLock()
// 	if chatroomCounter.wsCount != 1 {
// 		defer chatroomCounter.RUnlock()
// 		t.Fatalf("unexpected active connections, expect 1; have: %d", chatroomCounter.wsCount)
// 	}

// 	if chatroomCounter.callerStatus != initPeerStatus {
// 		defer chatroomCounter.RUnlock()
// 		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", chatroomCounter.callerStatus, initPeerStatus)
// 	}
// 	chatroomCounter.RUnlock()

// 	ws1, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	if resp.StatusCode != http.StatusSwitchingProtocols {
// 		t.Fatalf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
// 	}

// 	// sleep for counter update
// 	time.Sleep(500 * time.Millisecond)

// 	chatroomCounter.RLock()
// 	defer chatroomCounter.RUnlock()
// 	if chatroomCounter.wsCount != 2 {
// 		t.Fatalf("unexpected active connections, expect 2; have: %d", chatroomCounter.wsCount)
// 	}

// 	return
// }

func TestUserConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))

	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	t.Run("testMaximumConnections", func(t *testing.T) {
		testMaximumConnections(t, server)
	})
	t.Run("testReconnectCaller", func(t *testing.T) {
		testReconnectCaller(t, server)
	})
	t.Run("testReconnectCallee", func(t *testing.T) {
		testReconnectCallee(t, server)
	})
	// @todo need to refactor
	// t.Run("testCalleeUpgradeToCaller", func(t *testing.T) {
	// 	testCalleeUpgradeToCaller(t, server)
	// })
}
