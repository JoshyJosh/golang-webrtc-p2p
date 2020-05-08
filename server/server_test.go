package videocaller

import (
	"encoding/json"
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

	ws1, _, err := websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err != nil {
		t.Error(err)
		return
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

	_, _, err = websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Errorf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}

	// Third person should not be able to connect to peer to peer call
	_, _, err = websocket.DefaultDialer.Dial("ws://localhost:3000/websocket", nil)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Errorf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}
}

// // TestReconnectCaller test that if Caller drops connection,
// // a new connection is reinitialized
// func TestReconnectCaller(t *testing.T) {
// 	return
// }

// // TestReconnectCaller test that if Callee drops connection,
// // a new connection is reinitialized
// func TestReconnectCallee(t *testing.T) {
// 	return
// }
