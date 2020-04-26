/* eslint-env browser */

const WS = new WebSocket('ws://' + window.location.host + '/websocket')

let pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
    }
  ]
})
var log = msg => {
  document.getElementById('logs').innerHTML += msg + '<br>'
}

navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    stream.getTracks().forEach(track => pc.addTrack(track, stream));
    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  }).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
pc.onicecandidate = event => {
  if (event.candidate === null) {
    sessionDesc = btoa(JSON.stringify(pc.localDescription))
    document.getElementById('localSessionDescription').value = sessionDesc
    WS.send(JSON.stringify({type:"SessionDesc", data:sessionDesc}))
  }
}
pc.ontrack = function (event) {
  var el = document.createElement(event.track.kind)
  el.srcObject = event.streams[0]
  el.autoplay = true
  el.controls = true

  document.getElementById('remoteVideos').appendChild(el)
}

WS.onmessage = function(event) {
  // @todo decode and add message to remote description
  console.log("received message: " + event)
}

window.startSession = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
  } catch (e) {
    alert(e)
  }
}
