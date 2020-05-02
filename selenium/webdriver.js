// copied and modified from https://github.com/fippo/testbed/blob/master/webdriver.js

const webdriver = require('selenium-webdriver')
const firefox = require('selenium-webdriver/firefox');
const chrome = require('selenium-webdriver/chrome')
// const edge = require('selenium-webdriver/edge')
// const safari = require('selenium-webdriver/safari')

// selenium server, defaults to docker container
const selenium_server =  process.env.SELENIUM_SERVER || "http://127.0.0.1:4444/wd/hub"
const driver_script_timeout = process.env.DRIVER_TIMEOUT || 5000 // set in miliseconds

function buildDriver(browser, options) {
    let driver = new webdriver.Builder()
                              .setFirefoxOptions(new firefox.Options())
                              .setChromeOptions(new chrome.Options())
                              .forBrowser(browser)
                              .usingServer(selenium_server)

    if (browser === 'firefox') {
        driver.getCapabilities().set('marionette', true)
        driver.getCapabilities().set('acceptInsecureCerts', true)
    }

    // Set global executeAsyncScript() timeout (default is 0) to allow async
    // callbacks to be caught in tests.
    // driver.getCapabilities().set('timeouts.scripts', driver_script_timeout)  

    driver = driver.build()
    
    return driver
}

module.exports = {
    buildDriver: buildDriver,
}