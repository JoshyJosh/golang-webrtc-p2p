package videocaller

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testPeer struct {
	conn *websocket.Conn
}

// TestMaximumConnections tries to connect multiple users to websockets
// more than the maximum should be rejected
func TestMaximumConnections(t *testing.T) {
	go StartServer(":3000")

	time.Sleep(1 * time.Second)

	if wsCount != 0 {
		t.Errorf("unexpected active connections, expect 0; have: %d", wsCount)
		return
	}

	// first connection
	ws1, resp, err := websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err != nil {
		t.Error(err)
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 1 {
		t.Errorf("unexpected active connections, expect 1; have: %d", wsCount)
		return
	}

	msgType, msgBody, err := ws1.ReadMessage()
	if err != nil {
		t.Error(err)
		return
	}

	if msgType != websocket.TextMessage {
		t.Errorf("Unknown response message type: %d", msgType)
		return
	}

	msg1 := wsMsg{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Error(err)
		return
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Errorf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
		return
	}

	// second connection
	_, resp, err = websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err != nil {
		t.Error(err)
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Errorf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}

	// Third person should not be able to connect to peer to peer call
	_, resp, err = websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err == nil {
		t.Error("expected error in third response")
		return
	}

	if resp.StatusCode != http.StatusLocked {
		t.Errorf("unexpected third response status: %d; expected %d", resp.StatusCode, http.StatusLocked)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Errorf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}
}

// TestReconnectCaller test that if Caller drops connection,
// a new connection is reinitialized
// func TestReconnectCaller(t *testing.T) {
// 	return
// }

// // TestReconnectCaller test that if Callee drops connection,
// // a new connection is reinitialized
// func TestReconnectCallee(t *testing.T) {
// 	return
// }
