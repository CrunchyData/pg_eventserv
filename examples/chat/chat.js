var channelDefault = "public";
var wsUrlBase = "ws://localhost:7700/listen/";
var fsUrlBase = "http://localhost:9000";

// Set up the defaults and hook up the events
// once the page is finished loading.
window.onload = function() {
  var userRandom = Math.floor(Math.random() * 1000);
  var userDefault = "User-" + userRandom;
  document.getElementById("username").value = userDefault;
  document.getElementById("channel").value = channelDefault;
  document.getElementById("connect").onclick = wsConnect;
  document.getElementById("send").onclick = fsSend;
  document.getElementById("message").addEventListener("keypress", fsEnter);
  wsConnect();
}

// Send the current message on enter as well
// as on button click.
function fsEnter(event) {
  if (event.key == "Enter") {
    fsSend();
  }
}

// When the channel connect button is clicked
// (and at the end of the page load routine)
// we connect to the event server.
function wsConnect() {
  var channel = document.getElementById("channel").value;
  var wsUrl = wsUrlBase + channel;

  var outputStatus = document.getElementById("status");
  var outputChat = document.getElementById("textarea");
  var ws = new WebSocket(wsUrl);

  ws.onopen = function () {
    outputStatus.innerHTML = `Connected to <b>${channel}</b>.\n`;
  };

  ws.onerror = function(error) {
    console.log("WebSocket error: " + error.message);
  };

  // Got a message from the WebSocket!
  ws.onmessage = function (e) {

    // First, we can only handle JSON payloads, so quickly
    // try and parse it as JSON. Catch failures and return.
    try {
      var payload = JSON.parse(e.data);
    }
    catch (err) {
      outputChat.innerHTML = "Error, unable to parse WebSocket payload as JSON:\n\n";
      outputChat.innerHTML += e.data;
      return;
    }

    // We can only process payloads that have an expected
    // structure.
    if (!("user_name" in payload && "message" in payload)) {
      outputChat.innerHTML = "Error, unknown JSON object on channel:\n\n";
      outputChat.innerHTML += JSON.stringify(payload);
      return;
    }

    // Append the message content to the chat window!
    var user_name = payload.user_name;
    var message = payload.message;
    var message = `[${user_name}] ${message}\n`;
    outputChat.innerHTML += message;
    return;
  };
}

// We send new message into the system via the pg_featureserv
// function "message_send(username, channel, message)
function fsSend() {

  // Read the data from the form and encode it into
  // a URL data object.
  var prms = new URLSearchParams({
    username: document.getElementById("username").value,
    channel: document.getElementById("channel").value,
    message: document.getElementById("message").value
  });

  var fsUrl = `${fsUrlBase}/functions/postgisftw.message_send/items.json`;
  var xmlhttp = new XMLHttpRequest();
  xmlhttp.onreadystatechange = function() {
    if (xmlhttp.readyState == XMLHttpRequest.DONE) { // XMLHttpRequest.DONE == 4
        if (xmlhttp.status == 200) {
          // processed action, so clear the current message from the input
          document.getElementById("message").value = "";
        }
        else {
          // something bad happened, expose that to the user
          outputChat.innerHTML = `Error, send to ${fsUrlBase} failed.`;
      }
    }
  };
  xmlhttp.open("GET", fsUrl + "?" + prms.toString(), true);
  xmlhttp.send();
}
