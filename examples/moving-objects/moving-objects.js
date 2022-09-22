
// Connection information for the pg_eventserv
// This sends us updates on object location
var wsChannel = "objects";
var wsHost = "ws://localhost:7700";
var wsUrl = `${wsHost}/listen/${wsChannel}`;

// Connection information for the pg_featureserv
// This is where we send commands to move objects
var fsHost = "http://localhost:9000";
var fsObjsUrl = `${fsHost}/collections/moving.objects/items.json`;
var fsFencesUrl = `${fsHost}/collections/moving.geofences/items.json`;


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

// Get current set of moving objects from server
var objLayer = new ol.layer.Vector({
  source: new ol.source.Vector({
    url: fsObjsUrl,
    format: new ol.format.GeoJSON(),
  }),
  style: getIconStyle
});

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
objLayer.getSource().on('featuresloadend', objsAddToPage);



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


// Get current set of geofences from server
var fenceLayer = new ol.layer.Vector({
  source: new ol.source.Vector({
    url: fsFencesUrl,
    format: new ol.format.GeoJSON()
  }),
  style: new ol.style.Style({
    stroke: new ol.style.Stroke({
      color: 'blue',
      width: 2
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

ws.onmessage = function (e) {
  try {
    var payload = JSON.parse(e.data);
    outputStatus.innerHTML = JSON.stringify(payload, null, 2) + "\n";
  }
  catch (err) {
    outputStatus.innerHTML = "Error: Unable to parse JSON payload\n\n";
    outputStatus.innerHTML += e.data;
    return;
  }

  if ("object_id" in payload && "events" in payload && "location" in payload) {
    var oid = payload.object_id;
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
      // console.log(newFeature);
      objectSource.addFeature(newFeature);
    }
  }
  else if ( "layer" in payload && "change"  in payload ) {
    if ("geofences" == payload.layer) {
      fenceLayer.getSource().refresh();
    }
  }

};

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
