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

// @todo synchronize who is caller and who is callee
// navigator.mediaDevices.getUserMedia({ video: true, audio: true })
//   .then(stream => {
//     stream.getTracks().forEach(track => pc.addTrack(track, stream));
//     pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
//   }).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
pc.onicecandidate = event => {
  if (event.candidate === null) {
    sessionDesc = btoa(JSON.stringify(pc.localDescription))
    document.getElementById('localSessionDescription').value = sessionDesc
    WS.send(JSON.stringify({type:"CallerSessionDesc", data:sessionDesc}))
  }
}

window.initCaller = () => {
  navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    stream.getTracks().forEach(track => pc.addTrack(track, stream));
    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  }).catch(log)
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
  data = JSON.parse(event.data)
  switch (data.type) {
    case "InitCaller":
      initCaller()
      break
    case "CallerSessionDesc":
      document.getElementById('remoteSessionDescription').value = data.data
      initReceiver()
      break
    case "ReceiverSessionDesc":
      // @todo set function to recieve remote session description and initialize call
      break
    default:
      alert("invalid session description type: ", data.type)
  }
}

window.initReceiver = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
    .then(function(){
      return navigator.mediaDevices.getUserMedia({ video: true, audio: true })
    })
    .then(function(stream){
      localStream = stream

      document.getElementById("localVideos").srcObject = localStream
      localStream.getTracks().forEach(track => pc.addTrack(track, localStream))
    })
    .then(function() {
      return pc.createAnswer()
    })
    .then(function(answer){
      return pc.setLocalDescription(answer)
    })
    .catch(log)
    sessionDesc = btoa(JSON.stringify(pc.localDescription))
    WS.send(JSON.stringify({type:"ReceiverSessionDesc", data: sessionDesc}))
  } catch (e) {
    alert(e)
  }
}
