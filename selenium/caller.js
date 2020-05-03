const { buildDriver } = require('./webdriver')

let driver = buildDriver('firefox')

console.log(driver)

async function getWebRTCPage() {
    try {
        await driver.get('http://127.0.0.1:3000/')
        // sanity check
        // await driver.get('https://www.reddit.com')

        title = await driver.getTitle()
        console.log(title)
        
        source = await driver.getPageSource()
        console.log(source)
    } catch(err) {
        console.log(err)
    } finally {
        driver.quit()
    }
}

getWebRTCPage()
