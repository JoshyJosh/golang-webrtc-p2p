const { buildDriver } = require('./webdriver')
const test = require('tape')
const {until} = require('selenium-webdriver/lib/until')

class ReconnectPeerError extends Error {}

async function getWebRTCConnection(t, caller, receiver) {
    try {
        await caller.get('http://127.0.0.1:3000/')
        await receiver.get('http://127.0.0.1:3000/')

        await CheckStablePeerConnections(t, caller, receiver)
    } catch (err) {
        throw err
    }   
}

async function ReconnectPeer(t, peer) {
    try {
        await peer.navigate().refresh()
        
        await peer.executeScript("return pc")
                    .then(function(pc){
                        t.ok(pc.signalingState === "stable", "assert that peer connection has been reestablished")
                    })
    } catch (err) {
        throw new ReconnectPeerError(err)
    }
}

async function CheckStablePeerConnections(t, caller, receiver, additionalComment) {
    if (additionalComment) {
        additionalComment = " " + additionalComment
    } else {
        additionalComment = ""
    }

    try {
        // wait for peer connection to get established
        await caller.executeScript(`
            let checkLocalDescription = setInterval(() => {
                if (window.pc.currentRemoteDescription) {
                    // do something
                    if (window.pc.currentRemoteDescription.type === "offer") {
                        clearInterval(checkLocalDescription);
                    }
                }
            },500);
            
            setTimeout(() => { clearInterval(checkLocalDescription)}, 5000)
        `)
        await caller.executeScript("return pc") 
                    .then(function(pc){
                        t.ok(pc.signalingState === "stable", "assert that Caller has established webRTC connection" + additionalComment)
                        return pc
                    })
                    .then(function(pc){
                        t.ok(pc.currentLocalDescription !== null, "assert that Caller PeerConnection has non-null currentLocalDescription" + additionalComment)
                        t.equal(pc.currentLocalDescription.type, "offer", "assert that Caller PeerConnection has currentLocalDescription of type offer" + additionalComment)
                    })
                    .then(function(pc){
                        t.ok(pc.currentRemoteDescription !== null, "assert that Caller PeerConnection has non-null currentRemoteDescription" + additionalComment)
                        t.equal(pc.currentRemoteDescription.type, "answer", "assert that Caller PeerConnection has currentRemoteDescription of type answer" + additionalComment)
                    })
                    .catch(err => {
                        throw err
                    })
    
        await receiver.executeScript("return pc")
                      .then(function(pc){
                          t.ok(pc.signalingState === "stable", "assert that Receiver has established webRTC connection" + additionalComment)
                      })
                      .then(function(pc){
                          t.ok(pc.currentLocalDescription !== null, "assert that Receiver PeerConnection has non-null currentLocalDescription" + additionalComment)
                          t.equal(pc.currentLocalDescription.type, "answer", "assert that Receiver PeerConnection has currentLocalDescription of type answer" + additionalComment)
                      })
                      .then(function(pc){
                          t.ok(pc.currentRemoteDescription !== null, "assert that Receiver PeerConnection has non-null currentRemoteDescription" + additionalComment)
                          t.equal(pc.currentRemoteDescription.type, "offer", "assert that Receiver PeerConnection has currentRemoteDescription of type offer" + additionalComment)
                      })
                      .catch(err => {
                          throw err
                      })
    } catch(err) {
        throw err
    }
}

test("Firefox-SuccessfulConnection", async function (t){
    var driverCaller = buildDriver('firefox', undefined, false)
    var driverReceiver = buildDriver('firefox', undefined, false)

    try {
        await getWebRTCConnection(t, driverCaller, driverReceiver)
    } catch(err) {
        console.error(err)
    } finally {
        await driverCaller.quit()
        await driverReceiver.quit()
        t.end()
    }
})

test("Firefox-CallerReconnect", async function (t){
    var driverCaller = await buildDriver('firefox', undefined, false);
    var driverReceiver = await buildDriver('firefox', undefined, false);
    
    try {
        await getWebRTCConnection(t, driverCaller, driverReceiver)
        await ReconnectPeer(t, driverCaller)
        await CheckStablePeerConnections(t, driverCaller, driverReceiver, "after caller reconnect")
    } catch(err) {
        if (err instanceof ReconnectPeerError) {
            console.log("Peer connection error")
            await driverCaller.quit()
        }
        console.error(err)
    } finally {
        await driverCaller.quit()
        await driverReceiver.quit()
        t.end()
    }
})