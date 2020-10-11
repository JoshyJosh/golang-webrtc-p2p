/* eslint-env browser */
(function() {

window.WS = new WebSocket('ws://' + window.location.host + '/websocket') // could use wss://

window.startSession = function() {
  window.WS.onmessage = function(event) {
    // @todo decode and add message to remote description
    console.log(event)
    var data = JSON.parse(event.data)
    console.log(event.data)
    switch (data.type) {
      case "InitCaller":
        window.initCaller()
        break 
      case "CallerSessionDesc":
        document.getElementById('remoteSessionDescription').value = data.data
        window.initCallee()
        break
      case "CalleeSessionDesc":
        console.log("received ReceiverSessionDesc")
        remoteSDP = JSON.parse(atob(data.data))
        window.pc.setRemoteDescription(remoteSDP)
        // @todo set function to recieve remote session description and initialize call
        break
      case "ICECandidate":
        console.log("Getting ice candidate")
        var candidateMessage = JSON.parse(atob(data.data))
        window.incomingICEcandidate(candidateMessage)
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

  window.WS.send(JSON.stringify({type:"StartSession"}))
}

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

window.pc.oniceconnectionstatechange = e => {
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

window.pc.onsignalingstatechange = e => {
  switch(pc.signalingState) {
    case "closed":
      closeVideoCall()
      break
  }
}

window.pc.onicecandidate = (event) => {
  console.log("in onicecandidate")
  if (event.candidate) {
    console.log(event.candidate)
    WS.send(JSON.stringify({type:"ICECandidate", data:btoa(JSON.stringify(event.candidate))}))
  } else {
    console.log("empty candidate")
    return
  }
}

window.pc.onicegatheringstatechange = (event) => {
  let connection = event.target;

  switch(connection.iceGatheringState) {
    case "gathering":
      console.log("ice candidate still gathering")
      break
    case "complete":
      console.log("ice candidate gathering complete")
      break
  }
}

window.pc.onnegotiationneeded = () => {
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

window.pc.ontrack = function (event) {
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

