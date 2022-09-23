
// Connection information for the pg_eventserv
// This sends us updates on object location
var wsChannel = "objects";
var wsHost = "ws://localhost:7700";
var wsUrl = `${wsHost}/listen/${wsChannel}`;

// Connection information for the pg_featureserv
// This is where we send commands to move objects
// and where we draw the geofence and initial
// object locations from
var fsHost = "http://localhost:9000";
var fsObjsUrl = `${fsHost}/collections/moving.objects/items.json`;
var fsFencesUrl = `${fsHost}/collections/moving.geofences/items.json`;

// Objects are colored based on their
// 'color' property, so we need a dynamicly
// generated style to reflect that
var iconStyleCache = {};
function getIconStyle(feature) {
  var iconColor = feature.get('color');
  if (!iconStyleCache[iconColor]) {
    iconStyleCache[iconColor] = new ol.style.Style({
      image: new ol.style.RegularShape({
        fill: new ol.style.Fill({
          color: iconColor
        }),
        stroke: new ol.style.Stroke({
          width: 1,
          color: 'grey'
        }),
        points: 16,
        radius: 6,
        angle: Math.PI / 4
      })
    });
  }
  return iconStyleCache[iconColor];
};

// Download the current set of moving objects from
// the pg_featureserv
var objLayer = new ol.layer.Vector({
  source: new ol.source.Vector({
    url: fsObjsUrl,
    format: new ol.format.GeoJSON(),
  }),
  style: getIconStyle
});

// We need a visual panel for each object so we can
// click on up/down/left/right controls, so we dynamically
// build the panels for each record in the set we
// downloaded from pg_featureserv
function objsAddToPage() {
  objLayer.getSource().forEachFeature((feature) => {
      // console.log(feature);
      var id = feature.get('id');
      var objListElem = document.getElementById("objectList");
      var objElem = document.createElement("div");
      objElem.className = "object";
      objListElem.appendChild(objElem);
      var objIconId = `obj${id}icon`;
      var objHtml = `<div class="objectname">
        Object ${id} <span id="${objIconId}">&#11044;</span>
        </div>
        <div class="controls">
        <a href="#" onclick="objMove(${id},'left')">⬅️</a>
        <a href="#" onclick="objMove(${id},'up')">⬆️</a>
        <a href="#" onclick="objMove(${id},'down')">⬇️</a>
        <a href="#" onclick="objMove(${id},'right')">➡️</a>
        </div>`;
      objElem.innerHTML = objHtml;
      var iconElem = document.getElementById(objIconId);
      iconElem.style.color = feature.get('color');
      return false;
    }
  );
}
// Cannot build the HTML panels until the features have been
// fully downloaded.
objLayer.getSource().on('featuresloadend', objsAddToPage);


// When a control is clicked, we just need to hit the
// pg_featureserv function end point with the direction
// and object id. So we do not have any actions to take
// in the onreadystatechange method, actually.
function objMove(objId, direction) {
  //console.log(`move ${objId}! ${direction}`);
  var xmlhttp = new XMLHttpRequest();
  xmlhttp.onreadystatechange = function() {
      if (xmlhttp.readyState == XMLHttpRequest.DONE) { // XMLHttpRequest.DONE == 4
         if (xmlhttp.status == 200) {
            // processed move
         }
         else {
            // move failed
          }
      }
  };
  var objMoveUrl = `${fsHost}/functions/postgisftw.object_move/items.json?direction=${direction}&move_id=${objId}`;
  xmlhttp.open("GET", objMoveUrl, true);
  xmlhttp.send();
}


// Get current set of geofences from the
// pg_featureserv
var fenceLayer = new ol.layer.Vector({
  source: new ol.source.Vector({
    url: fsFencesUrl,
    format: new ol.format.GeoJSON()
  }),
  style: new ol.style.Style({
    stroke: new ol.style.Stroke({
      color: 'blue',
      width: 3
    }),
    fill: new ol.style.Fill({
      color: 'rgba(0, 0, 255, 0.1)'
    })
  })
});

// Basemap tile layer
var baseLayer = new ol.layer.Tile({
  source: new ol.source.XYZ({
    url: "https://maps.wikimedia.org/osm-intl/{z}/{x}/{y}.png"
  })
});

// Compose map of our three layers
var map = new ol.Map({
  target: 'map',
  view: new ol.View({
    center: [0, 0],
    zoom: 2
  }),
  layers: [baseLayer
    ,fenceLayer
    ,objLayer
  ]
});

var outputStatus = document.getElementById("wsStatus");
console.log("Preparing WebSocket...");
var ws = new WebSocket(wsUrl);
console.log("WebSocket created.");

ws.onopen = function () {
  outputStatus.innerHTML = "Connected to WebSocket!\n";
};

ws.onerror = function(error) {
  console.log(`[error] ${error.message}`);
};

// Got a message from the WebSocket!
ws.onmessage = function (e) {

  // First, we can only handle JSON payloads, so quickly
  // try and parse it as JSON. Catch failures and return.
  try {
    var payload = JSON.parse(e.data);
    outputStatus.innerHTML = JSON.stringify(payload, null, 2) + "\n";
  }
  catch (err) {
    outputStatus.innerHTML = "Error: Unable to parse JSON payload\n\n";
    outputStatus.innerHTML += e.data;
    return;
  }

  // We are not segmenting payloads by channel here, so we
  // test the 'type' property to find out what kind of
  // payload we are dealing with.
  if ("type" in payload && payload.type == "objectchange") {
    var oid = payload.object_id;

    // The map sends us back coordinates in the map projection,
    // which is web mercator (EPSG:3857) since we are using
    // a web mercator back map. That means a little back projection
    // before we start using the coordinates.
    var lng = payload.location.longitude;
    var lat = payload.location.latitude;
    var coord = ol.proj.transform([lng, lat], 'EPSG:4326', 'EPSG:3857');
    const objGeom = new ol.geom.Point(coord);
    const objProps = {
      timeStamp: payload.ts,
      props: payload.props,
      color: payload.color,
    };
    var objectSource = objLayer.getSource();

    // Make sure we already have this object in our
    // local data source. If we do, we update the object,
    // if we do not, we create a fresh local object and
    // add it to our source.
    const curFeature = objectSource.getFeatureById(oid);
    if (curFeature) {
      curFeature.setGeometry(objGeom);
      curFeature.setProperties(objProps);
      // console.log(curFeature);
    }
    else {
      const newFeature = new ol.Feature(objGeom);
      newFeature.setProperties(objProps);
      newFeature.setId(oid);
      objectSource.addFeature(newFeature);
      // console.log(newFeature);
    }

    // Watch out for enter/leave events and change the color
    // on the appropriate geofence to match the object
    // doing the entering/leaving
    if (payload.events) {
      // Dumbed down to only handle on event at a time
      var event = payload.events[0];
      var fenceId = event.geofence_id;
      var feat = fenceLayer.getSource().getFeatureById(fenceId);
      var style = feat.getStyle() ? feat.getStyle() : fenceLayer.getStyle().clone();
      style.getStroke().setColor(event.action == "entered" ? payload.color : "blue");
      feat.setStyle(style);
    }
  }

  // Watch for a "layer changed" payload and fully reload the
  // data for the appropriate layer when it comes by. Generally
  // useful for all kinds of map synching needs.
  if ( "type" in payload && payload.type == "layerchange") {
    if ("geofences" == payload.layer) {
      fenceLayer.getSource().refresh();
    }
  }

};

// This is that the payload object looks like, it's only
// one possibility among many.

// {
// 'object_id': 1,
// 'events': [
//   {
//     'action': 'left',
//     'geofence_id': 3,
//     'geofence_label': 'Ranch'
//   }
// ],
// 'location': {
//   'longitude': -126.4,
//   'latitude': 45.3,
//   }
// 'ts': '2001-01-01 12:34:45.1234',
// 'props': {'name':'Paul'},
// 'color': 'red'
// }
