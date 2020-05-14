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

function closeVideoCall() {
  let remoteVideo = document.getElementById("received_video")
  let localVideo = document.getElementById("local_video")

  if (remoteVideo.srcObject) {
    remoteVideo.srcObject.getTracks().forEach(track => track.stop());
  }

  if (localVideo.srcObject) {
    localVideo.srcObject.getTracks().forEach(track => track.stop());
  }

  pc.close()

  remoteVideo.removeAttribute("src");
  remoteVideo.removeAttribute("srcObject");
  localVideo.removeAttribute("src");
  remoteVideo.removeAttribute("srcObject");
}

pc.oniceconnectionstatechange = e => {
  log(pc.iceConnectionState)

  switch(pc.iceConnectionState) {
    case "disconnected":
    case "closed":
    case "failed":
      closeVideoCall()
      break
  }
}

pc.onsignalingstatechange = e => {
  switch(pc.signalingState) {
    case "closed":
      closeVideoCall()
      break
  }
}

pc.onicecandidate = event => {
  console.log("in onicecandidate")
  if (event.candidate) {
    console.log(event.candidate)
    WS.send(JSON.stringify({type:"ICECandidate", data:btoa(JSON.stringify(event.candidate))}))
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
  console.log("in init caller")
  navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  .then(stream => {
    stream.getTracks().forEach(track => pc.addTrack(track, stream))
    document.getElementById("localVideos").srcObject = stream
    document.getElementById("localVideos").autoplay = true
  }).catch(log)
}

window.incomingICEcandidate = (msg) => {
  var candidate = new RTCIceCandidate(msg)

  pc.addIceCandidate(candidate).catch(log)
}

pc.ontrack = function (event) {
  console.log("in ontrack event")
  if (document.getElementById('remoteVideos').srcObject) {
    document.getElementById('remoteVideos').srcObject.addTrack(event.track)
    return
  }
  document.getElementById('remoteVideos').srcObject = event.streams[0]
  document.getElementById('remoteVideos').autoplay = true
}

WS.onmessage = function(event) {
  // @todo decode and add message to remote description
  console.log(event)
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
      remoteSDP = JSON.parse(atob(data.data))
      pc.setRemoteDescription(remoteSDP)
      // @todo set function to recieve remote session description and initialize call
      break
    case "ICECandidate":
      console.log("Getting ice candidate")
      var candidateMessage = JSON.parse(atob(data.data))
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
    document.getElementById("localVideos").autoplay = true
    stream.getTracks().forEach(track => pc.addTrack(track, stream))
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

