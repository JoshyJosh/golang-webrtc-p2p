/* eslint-env browser */
(function() {

const WS = new WebSocket('ws://' + window.location.host + '/websocket')
window.WS = WS

window.pc = new RTCPeerConnection({
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
  console.log("in onicecandidate")
  if (event.candidate) {
    console.log(event.candidate)
    WS.send(JSON.stringify({type:"ICECandidate", data:JSON.stringify(btoa(event.candidate))}))
  }
}

pc.onnegotiationneeded = () => {
  console.log("in onnegotiationneeded")
  pc.createOffer().then(offer => pc.setLocalDescription(offer))
  .then(() => {
    sessionDesc = btoa(JSON.stringify(pc.localDescription))
    WS.send(JSON.stringify({type:"CallerSessionDesc", data:sessionDesc}))
    document.getElementById('localSessionDescription').value = sessionDesc
  })
  .catch(log)
}

window.initCaller = () => {
  navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    stream.getTracks().forEach(track => pc.addTrack(track, stream))
    document.getElementById("localVideos").srcObject = stream
  }).catch(log)
}

window.incomingICEcandidate = (msg) => {
  var candidate = new RTCIceCandidate(msg.candidate)

  pc.addIceCandidate(candidate).catch(log)
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
  var data = JSON.parse(event.data)
  console.log(event.data)
  switch (data.type) {
    case "InitCaller":
      initCaller()
      break
    case "CallerSessionDesc":
      document.getElementById('remoteSessionDescription').value = data.data
      initReceiver()
      break
    case "ReceiverSessionDesc":
      console.log("received ReceiverSessionDesc")
      // @todo set function to recieve remote session description and initialize call
      break
    case "ICECandidate":
      console.log("Getting ice candidate")
      var candidateMessage = JSON.parse(btoa(data))
      incomingICEcandidate(candidateMessage)
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

  pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
  .then(function(){
    return navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  })
  .then(function(stream){
    document.getElementById("localVideos").srcObject = stream
    localStream.getTracks().forEach(track => pc.addTrack(track, stream))
  })
  .then(function() {
    return pc.createAnswer()
  })
  .then(function(answer){
    return pc.setLocalDescription(answer)
  })
  .then(function() {
    sessionDesc = btoa(JSON.stringify(pc.localDescription))
    WS.send(JSON.stringify({type:"ReceiverSessionDesc", data: sessionDesc}))
  })
  .catch(log)
}
})()

