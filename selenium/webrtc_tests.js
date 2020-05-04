const { buildDriver } = require('./webdriver')
const test = require('tape')

let driverCaller = buildDriver('firefox', undefined, false)
let driverReceiver = buildDriver('firefox', undefined, false)

console.log(driverCaller)
console.log(driverReceiver)

async function getWebRTCPage(t) {
    try {
        await driverCaller.get('http://127.0.0.1:3000/')
        await driverReceiver.get('http://127.0.0.1:3000/')
        // sanity check
        // await driver.get('https://www.reddit.com')

        title = await driverCaller.getTitle()
        console.log(title)
        
        source = await driverCaller.getPageSource()
        console.log(source)

        driverCaller.sleep(2000)

        await driverCaller.executeScript("return pc")
                          .then(function(pc){
                              console.log(pc)
                              t.ok(pc.signalingState === "stable", "assert that Caller has established webRTC connection")
                          })
        
        await driverReceiver.executeScript("return pc")
                            .then(function(pc){
                                console.log(pc)
                                t.ok(pc.signalingState === "stable", "assert that Receiver has established webRTC connection")
                            })
    } catch(err) {
        console.log(err)
    } finally {
        driverCaller.quit()
        driverReceiver.quit()
    }

    t.end()
}



test("Firefox-FullConnection", t => {
    getWebRTCPage(t)
})
