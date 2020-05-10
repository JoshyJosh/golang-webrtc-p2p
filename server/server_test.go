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

func testServerTeardown(t testing.T, server *httptest.Server, ws1, ws2, ws3 *websocket.Conn) {
	t.Log("in testServerTeardown")

	server.CloseClientConnections()
	server.Close()

	// cannot defer during the test, must do Closes on teardown
	if ws1 != nil {
		ws1.Close()
	}
	if ws2 != nil {
		ws2.Close()
	}
	if ws3 != nil {
		ws3.Close()
	}

	return
}

func checkInitialServerStatus(t testing.T) {
	if wsCount != 0 {
		t.Fatalf("unexpected active connections, expect 0; have: %d", wsCount)
	}

	if callerStatus != callerUnsetStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}

	return
}

func firstWSConnection(t testing.T, wsUrl string) (ws websocket.Conn) {
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

	if wsCount != 1 {
		t.Fatalf("unexpected active connections, expect 1; have: %d", wsCount)
	}

	msgType, msgBody, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("unknown response message type: %d", msgType)
	}

	msg1 := wsMsg{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
	}

	if callerStatus != callerInitStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}

	return
}

// TestMaximumConnections tries to connect multiple users to websockets
// more than the maximum should be rejected
func TestMaximumConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	var ws1, ws2, ws3 *websocket.Conn

	defer func() {
		testServerTeardown(*t, server, ws1, ws2, ws3)
	}()

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	checkInitialServerStatus(*t)

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

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
	}

	// Third person should not be able to connect to peer to peer call
	ws3, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err == nil {
		t.Fatal("expected error in third response")
	}

	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("unexpected third response status: %d; expected %d", resp.StatusCode, http.StatusLocked)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
	}
}

// TestReconnectCaller test that if Caller drops connection,
// a new connection is reinitialized
func TestReconnectCaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	var ws1 *websocket.Conn

	defer func() {
		testServerTeardown(*t, server, ws1, nil, nil)
	}()

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	checkInitialServerStatus(*t)

	// first connection
	wstmp := firstWSConnection(*t, wsUrl)
	ws1 = &wstmp

	ws1.Close()

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	if wsCount != 0 {
		t.Fatalf("unexpected number of active connections, expect 0; have: %d", wsCount)
	}

	if callerStatus != callerUnsetStatus {
		t.Fatalf("unexpected callerStatus for first connection disconnect: %s; expected %s", callerStatus, callerInitStatus)
	}

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

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	if wsCount != 1 {
		t.Fatalf("unexpected number of active connections, expect 1; have: %d", wsCount)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
	}

	msg1 := wsMsg{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
	}

	if callerStatus != callerInitStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}
}

// TestReconnectCaller test that if Callee drops connection,
// a new connection is reinitialized
func TestReconnectCallee(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	var ws1, ws2 *websocket.Conn

	defer func() {
		testServerTeardown(*t, server, ws1, ws2, nil)
	}()

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	// check server status
	checkInitialServerStatus(*t)

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

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
	}

	if callerStatus != callerInitStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}

	ws2.Close()

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	if wsCount != 1 {
		t.Fatalf("unexpected active connections, expect 1; have: %d", wsCount)
	}

	if callerStatus != callerInitStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}

	ws2, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// sleep for counter update
	time.Sleep(500 * time.Millisecond)

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
	}

	if callerStatus != callerInitStatus {
		t.Fatalf("unexpected callerStatus for first connection: %s; expected %s", callerStatus, callerInitStatus)
	}
}
