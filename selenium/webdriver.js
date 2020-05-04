// copied and modified from https://github.com/fippo/testbed/blob/master/webdriver.js

const webdriver = require('selenium-webdriver')
const firefox = require('selenium-webdriver/firefox')
const chrome = require('selenium-webdriver/chrome')
const os = require('os')
// const edge = require('selenium-webdriver/edge')
// const safari = require('selenium-webdriver/safari')

// selenium server, defaults to docker container
const selenium_server =  process.env.SELENIUM_SERVER || "http://127.0.0.1:4444/wd/hub"
const driver_script_timeout = process.env.DRIVER_TIMEOUT || 5000 // set in miliseconds

function buildDriver(browser, options, grid) {

    let profile
    options = options || {}
    if (options.firefoxprofile) {
        profile = new firefox.Profile(options.firefoxprofile)
        if (typeof options.h264 === 'string') {
        profile.setPreference('media.gmp-gmpopenh264.version', options.h264) // openh264
        } else if (options.h264 !== false) {
        profile.setPreference('media.gmp-gmpopenh264.version', '1.6') // openh264
        }
    } else if (options.h264) {
        // contains gmp-gmpopenh264/1.6 which may contain openh264 binary.
        profile = new firefox.Profile('h264profile')
        profile.setPreference('media.gmp-gmpopenh264.version', '1.6') // openh264
    } else {
        profile = new firefox.Profile()
    }
    profile.setAcceptUntrustedCerts(true)

    profile.setPreference('media.navigator.streams.fake', true)
    profile.setPreference('media.navigator.permission.disabled', true)
    profile.setPreference('xpinstall.signatures.required', false)
    profile.setPreference('media.peerconnection.dtls.version.min', 771) // force DTLS 1.2
    if (options.disableFirefoxWebRTC) {
        profile.setPreference('media.peerconnection.enabled', false)
    }

    if (options.devices && options.devices.extension) {
        profile.addExtension(options.devices.extension)
    }

    const firefoxOptions = new firefox.Options()
        .setProfile(profile)
    let firefoxPath
    if (options.firefoxpath) {
        firefoxPath = options.firefoxpath
    } else if (!grid) {
        if (os.platform() == 'linux' && options.bver) {
        firefoxPath = 'browsers/bin/firefox-' + options.bver
        }
    }
    const firefoxBinary = new firefox.Binary(firefoxPath)
    if (options.headless) {
        firefoxBinary.addArguments('-headless')
    }
    firefoxOptions.setBinary(firefoxBinary)

    // Chrome options.
    let chromeOptions = new chrome.Options()
        .addArguments('enable-features=WebRTC-H264WithOpenH264FFmpeg')
        .addArguments('allow-file-access-from-files')
        .addArguments('allow-insecure-localhost')
        .addArguments('use-fake-device-for-media-stream')
        .addArguments('disable-translate')
        .addArguments('no-process-singleton-dialog')
        // .addArguments('disable-dev-shm-usage')
        .addArguments('mute-audio')
    if (options.experimental !== false) {
        chromeOptions.addArguments('enable-experimental-web-platform-features')
    }
    if (options.headless) {
        chromeOptions.addArguments('headless')
        chromeOptions.addArguments('disable-gpu')
    }
    if (options.noSandbox) {
        chromeOptions.addArguments('no-sandbox')
    }
    if (options.chromeFlags) {
        options.chromeFlags.forEach((flag) => chromeOptions.addArguments(flag))
    }
    // ensure chrome.runtime is visible.
    chromeOptions.excludeSwitches('test-type')

    if (options.android) {
        chromeOptions = chromeOptions.androidChrome()
    } else if (options.chromepath) {
        chromeOptions.setChromeBinaryPath(options.chromepath)
    } else if (!grid && os.platform() === 'linux' && options.bver) {
        chromeOptions.setChromeBinaryPath('browsers/bin/chrome-' + options.bver)
    }

    if (!options.devices || options.headless || options.android) {
        // GUM doesn't work in headless mode so we need this. See 
        // https://bugs.chromium.org/p/chromium/issues/detail?id=776649
        chromeOptions.addArguments('use-fake-ui-for-media-stream')
    } else {
        // see https://bugs.chromium.org/p/chromium/issues/detail?id=459532#c22
        const domain = 'https://' + (options.devices.domain || 'localhost') + ':' + (options.devices.port || 443) + ',*'
        const exceptions = {
        media_stream_mic: {},
        media_stream_camera: {}
        }

        exceptions.media_stream_mic[domain] = {
        last_used: Date.now(),
        setting: options.devices.audio ? 1 : 2 // 0: ask, 1: allow, 2: denied
        }
        exceptions.media_stream_camera[domain] = {
        last_used: Date.now(),
        setting: options.devices.video ? 1 : 2
        }

        chromeOptions.setUserPreferences({
        profile: {
            content_settings: {
            exceptions: exceptions
            }
        }
        })
    }

    if (options.devices) {
        if (options.devices.screen) {
        chromeOptions.addArguments('auto-select-desktop-capture-source=' + options.devices.screen)
        }
        if (options.devices.extension) {
        chromeOptions.addArguments('load-extension=' + options.devices.extension)
        }
    }


    let driver = new webdriver.Builder()
                              .setFirefoxOptions(firefoxOptions)
                              .setChromeOptions(chromeOptions)
                              .forBrowser(browser)
                            //   .usingServer(selenium_server)

    // if (browser === 'firefox') {
    //     driver.getCapabilities().set('moz:firefoxOptions', options)
    driver.getCapabilities().set('marionette', true)
    //     driver.getCapabilities().set('acceptInsecureCerts', true)
    // }

    // Set global executeAsyncScript() timeout (default is 0) to allow async
    // callbacks to be caught in tests.
    // driver.getCapabilities().set('timeouts.scripts', driver_script_timeout)  

    driver = driver.build()

    async function getCaps() {
        caps = await driver.getCapabilities()   
        console.log(caps)
    }

    getCaps()
    
    return driver
}

module.exports = {
    buildDriver: buildDriver,
}