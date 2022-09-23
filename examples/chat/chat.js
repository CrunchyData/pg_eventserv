var channelDefault = "public";
var wsUrlBase = "ws://localhost:7700/listen/";
var fsUrlBase = "http://localhost:9000";

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

function fsEnter(event) {
  if (event.key == "Enter") {
    fsSend();
  }
}

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

    if (!("user_name" in payload && "message" in payload)) {
      outputChat.innerHTML = "Error, unknown JSON object on channel:\n\n";
      outputChat.innerHTML += JSON.stringify(payload);
      return;
    }

    var user_name = payload.user_name;
    var message = payload.message;
    var message = `[${user_name}] ${message}\n`;
    outputChat.innerHTML += message;
    return;
  };
}

function fsSend() {

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
          // processed move
          document.getElementById("message").value = "";
        }
        else {
          outputChat.innerHTML = `Error, send to ${fsUrlBase} failed.`;
      }
    }
  };
  xmlhttp.open("GET", fsUrl + "?" + prms.toString(), true);
  xmlhttp.send();
}
