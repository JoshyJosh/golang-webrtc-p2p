/* eslint-env browser */
(function() {

window.WS = new WebSocket('ws://' + window.location.host + '/websocket') // could use wss://
// webrtc WS is no longer able to ping when establishing webrtc connection will use second websocket for healthcheck

window.pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302' // can have local server instead if more control is needed
    }
  ]
})

var log = msg => {
  document.getElementById('logs').innerHTML += msg + '<br>'
}

function closeVideoCall() {
  let remoteVideo = document.getElementById("remoteVideo")
  let localVideo = document.getElementById("localVideo")

  if (remoteVideo.srcObject) {
    remoteVideo.srcObject.getTracks().forEach(track => track.stop())
  }

  if (localVideo.srcObject) {
    localVideo.srcObject.getTracks().forEach(track => track.stop())
  }

  pc.close()

  remoteVideo.removeAttribute("src")
  remoteVideo.removeAttribute("srcObject")
  localVideo.removeAttribute("src")
  localVideo.removeAttribute("srcObject")
  
  // Remote srcObject completely
  remoteVideo.srcObject = null
}

pc.oniceconnectionstatechange = e => {
  log(pc.iceConnectionState)

  switch(pc.iceConnectionState) {
    case "completed":
      console.log("WebRTC connection completed closing WS")
      // When WebRTC has been fully established close the WS connection
      WS.close()
      break
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
    document.getElementById("localVideo").srcObject = stream
    document.getElementById("localVideo").autoplay = true
  }).catch(log)
}

window.incomingICEcandidate = (msg) => {
  var candidate = new RTCIceCandidate(msg)

  pc.addIceCandidate(candidate).catch(log)
}

pc.ontrack = function (event) {
  console.log("in ontrack event")
  if (document.getElementById('remoteVideo').srcObject) {
    document.getElementById('remoteVideo').srcObject.addTrack(event.track)
    return
  }
  document.getElementById('remoteVideo').srcObject = event.streams[0]
  document.getElementById('remoteVideo').autoplay = true

  // after WebRTC connection is established close websocket connection
  // WS.close()
}


WS.onmessage = function(event) {
  // @todo decode and add message to remote description
  console.log(event)
  var data = JSON.parse(event.data)
  console.log(event.data)
  switch (data.type) {
    // case "healthCheck":
      
    //   window.WS2.send(JSON.stringify({type:"ConnectionID", data: data.data}))
    //   console.log(window.WS2)
    //   startWS2OnMessage()
    case "InitCaller":
      initCaller()
      break 
    case "CallerSessionDesc":
      document.getElementById('remoteSessionDescription').value = data.data
      initCallee()
      break
    case "CalleeSessionDesc":
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
    case "UpgradeToCaller":
      console.log("Upgrating to Caller")
      WS.send(JSON.stringify({type:"UpgradeToCaller", data:sessionDesc}))
      break
    default:
      alert("invalid session description type: ", data.type)
  }
  console.log("At the end")
}

window.initCallee = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
  .then(function(){
    return navigator.mediaDevices.getUserMedia({ video: true, audio: true })
  })
  .then(function(stream){
    document.getElementById("localVideo").srcObject = stream
    document.getElementById("localVideo").autoplay = true
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
    WS.send(JSON.stringify({type:"CalleeSessionDesc", data: sessionDesc}))
  })
  .catch(log)
}
})()

