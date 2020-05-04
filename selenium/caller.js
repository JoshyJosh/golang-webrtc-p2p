const { buildDriver } = require('./webdriver')

let driverCaller = buildDriver('firefox', undefined, false)
let driverReceiver = buildDriver('firefox', undefined, false)

console.log(driverCaller)
console.log(driverReceiver)

async function getWebRTCPage() {
    try {
        await driverCaller.get('http://127.0.0.1:3000/')
        await driverReceiver.get('http://127.0.0.1:3000/')
        // sanity check
        // await driver.get('https://www.reddit.com')

        title = await driverCaller.getTitle()
        console.log(title)
        
        source = await driverCaller.getPageSource()
        console.log(source)
    } catch(err) {
        console.log(err)
    } finally {
        driverCaller.quit()
        driverReceiver.quit()
    }
}

getWebRTCPage()
