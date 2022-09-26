
var wsChannelDefault = "changes";
var wsUrlBase = "ws://localhost:7700/listen/";

// Set up the defaults and hook up the events
// once the page is finished loading.
window.onload = function() {
  wsConnect();
}

// When the channel connect button is clicked
// (and at the end of the page load routine)
// we connect to the event server.
function wsConnect() {
  var wsUrl = wsUrlBase + wsChannelDefault;

  var outputStatus = document.getElementById("status");
  var outputMessage = document.getElementById("textarea");

  var ws = new WebSocket(wsUrl);
  ws.onopen = function () {
    outputStatus.innerHTML = `Connected to "${wsChannelDefault}".`;
  };

  ws.onerror = function(error) {
    console.log("WebSocket error: " + error.message);
  };

  ws.onclose = function () {
    outputStatus.innerHTML = `Disconnected.`;
  };

  // Got a message from the WebSocket!
  ws.onmessage = function (e) {
    // First, we can only handle JSON payloads, so quickly
    // try and parse it as JSON. Catch failures and return.
    try {
      var payload = JSON.parse(e.data);
      outputMessage.innerHTML = JSON.stringify(payload, null, 2);
      return;
    }
    catch (err) {
      outputMessage.innerHTML = e.data;
      return;
    }

  };
}

