const { buildDriver } = require('./webdriver')
const test = require('tape')

async function getWebRTCConnection(t, caller, receiver) {
    try {
        await caller.get('http://127.0.0.1:3000/')
        await receiver.get('http://127.0.0.1:3000/')
        // sanity check
        // await driver.get('https://www.reddit.com')

        title = await caller.getTitle()
        console.log(title)
        
        source = await caller.getPageSource()
        console.log(source)

        await CheckStablePeerConnections(t, caller, receiver)
    } catch (err) {
        console.log(err)
    }

    // @todo this should be at the end of the test case
    caller.quit()
    receiver.quit()
    t.end()
}

async function ReconnectPeer(t, peer) {
    peer.close()
    peer.get('http://127.0.0.1:3000/')
}

async function CheckStablePeerConnections(t, caller, receiver, additionalComment) {
    callerComment = "assert that Caller has established webRTC connection"
    receiverComment = "assert that Receiver has established webRTC connection"

    // add additional commment to distinguish between test scenarios
    if (additionalComment) {
        callerComment += " " + additionalComment
        receiverComment += " " + additionalComment
    }

    await caller.executeScript("return pc")
                      .then(function(pc){
                          console.log(pc)
                          t.ok(pc.signalingState === "stable", callerComment)
                      })
    
    await receiver.executeScript("return pc")
                        .then(function(pc){
                            console.log(pc)
                            t.ok(pc.signalingState === "stable", receiverComment)
                        })
}

test("Firefox-SuccessfulConnection", t => {
    var driverCaller = buildDriver('firefox', undefined, false)
    var driverReceiver = buildDriver('firefox', undefined, false)

    try {
        getWebRTCConnection(t, driverCaller, driverReceiver)
    } catch(err) {
        console.log(err)
    } finally {
        // driverCaller.quit()
        // driverReceiver.quit()
    }    
})

// test("Firefox-CallerReconnect", t => {
//     let driverCaller = buildDriver('firefox', undefined, false)
//     let driverReceiver = buildDriver('firefox', undefined, false)

//     try {
//         getWebRTCConnection(t, driverCaller, driverReceiver)
//         ReconnectPeer(t, driverCaller)
//         CheckStablePeerConnections(t, driverCaller, driverReceiver, "after caller reconnect")
//     } catch(err) {
//         console.log(err)
//     } finally {
//         driverCaller.quit()
//         driverReceiver.quit()
//     }

//     t.end()
// })