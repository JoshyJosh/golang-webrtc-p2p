# Simple goland webrtc p2p program

This is a project made to test out a simple webrtc program with a golang server for initial client negotiations.

NOTE: This is currently a work in progress and a learning prototype overall. The server and needs to be manually restarted to create a new session once a disconnect has occured.

To run the server go to `server` folder and: 
 - run `go run cmd/main.go` with optional `--address=:<port number>` flag *OR*
 - run `make` to run server on with flag `--address=:3000`

Server can also be run via docker although it is currently automatically bound to port `3000`.

There are two main test suites in the repo one is a node implementation of selenium, which requires the server to be run beforehand. To run start a server in a separate thread and then run `node webrtc_tests.js`. 
Selenium tests are based on `https://github.com/fippo/testbed/blob/master/webdriver.js`, with slight modifications for client variations
The selenium tests are only run on firefox local geckodriver for now, since I had issues with the selenium docker grid setup.

Tests are currently still being worked on since reconnects are still being debugged.

to run tests go to `server` folder and use `make test`, the tests are supposed to not be run with more than one parallel  test routine and has to use explicit `videocaller` module in test command in order to use private handlers and variables.

Go test issues:
- Clients timeout during tests, even though on manual testing the same issues are not reproducable (possible httptest setup). Adding manual timeouts yielded no new results
- `testCalleeUpgradeToCaller` still needs to be refactored

Selenium test issues:
- Second test is failing since the Callee reconnect can only occur if getUserMedia tracks have not been accepted.
- currently only firefox tests, should ideally use the selenium grid dockerfile

## Sources:
### This is my current sourcelist, thanks to all the authors on the list so that i can fool around with possible implementations:
- https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Connectivity (connection overview)
- https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Signaling_and_video_calling (connection overview)
- https://www.html5rocks.com/en/tutorials/webrtc/basics/ (connection overview)
- https://github.com/fippo/testbed (testing)
- https://webrtc.ventures/2018/07/which-multi-party-webrtc-option-should-you-go-with/ (all sorts of webRTC architecture types)
- https://hub.docker.com/r/rwgrim/stuntman-server/dockerfile (stuntman server dockerfile, in case you want to host your own STUN and/or TURN server)