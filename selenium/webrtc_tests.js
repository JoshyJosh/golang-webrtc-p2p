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
        // @todo double check wait for peer connection to get established

        callerPc = await caller.executeScript("return pc") 

        await t.ok(callerPc.signalingState === "stable", "assert that Caller has established webRTC connection" + additionalComment)

        await t.ok(callerPc.currentLocalDescription !== null, "assert that Caller PeerConnection has non-null currentLocalDescription" + additionalComment)
        await t.equal(callerPc.currentLocalDescription.type, "offer", "assert that Caller PeerConnection has currentLocalDescription of type offer" + additionalComment)
    
        await t.ok(callerPc.currentRemoteDescription !== null, "assert that Caller PeerConnection has non-null currentRemoteDescription" + additionalComment)
        await t.equal(callerPc.currentRemoteDescription.type, "answer", "assert that Caller PeerConnection has currentRemoteDescription of type answer" + additionalComment)
    
        receiverPc = await receiver.executeScript("return pc")
    
        await t.ok(pc.signalingState === "stable", "assert that Receiver has established webRTC connection" + additionalComment)
    
        await t.ok(pc.currentLocalDescription !== null, "assert that Receiver PeerConnection has non-null currentLocalDescription" + additionalComment)
        await t.equal(pc.currentLocalDescription.type, "answer", "assert that Receiver PeerConnection has currentLocalDescription of type answer" + additionalComment)
    
        await t.ok(pc.currentRemoteDescription !== null, "assert that Receiver PeerConnection has non-null currentRemoteDescription" + additionalComment)
        await t.equal(pc.currentRemoteDescription.type, "offer", "assert that Receiver PeerConnection has currentRemoteDescription of type offer" + additionalComment)
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