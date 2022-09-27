# Moving Objects Map Example

This example combines a database-side state model of moving objects with a client-side map that displays the latest state of the system at all times.

We use the client-side map to add "motion" to the system by manipulating the location of the objects with simple up/down/left/right controls, but a "real" moving objects system would probably manipulate the object state by streaming in changes from GPS or other location aware devices.

Check out the database tables and functions in [moving-objects.sql](moving-objects.sql).

## Tables

* `objects` is where the current state of the moving objects resides. The current location is in a `geography` column, and the current geofences it intersects with is in an integer array `fences[]`.
* `objects_history` is the history of all states of the moving objects. Every time a moving object in the `objects` table is updated, the value is also written into the history table. The result is a queryable history of the system.
* `geofences` is a simple table of polygons. Each polygon has a unique identifier. Whenever an object moves, its location is checked against this table, to determine what fences it might reside in. If the set of fences the object is in has changed since the last update, extra information about the object entering/leaving the geofence is added to the update record send out to the clients.

## Functions

The "dynamic" nature of the moving object system is a result of the functions that watch for changes in the objects table and then carry out actions based on the new locations of the objects.

### Trigger Functions

The trigger functions are hooked up to tables, and fire when something changes in the table data.

* `objects_geofence()` add the current list of geofence identifiers a moving object intersects with to the update record.
* `objects_update()` adds a new record to the `objects_history` table on every update to the `objects` table. It also compares the current list of geofences the object is in with the previous list: differences indicate fences that the object has entered or left. It also bundles up the new location and the geofence events, and sends them to the `objects` channel using `pg_notify()`. From there, `pg_eventserv` pushing the notification out to any listening WebSockets clients.
* `layer_change()` is a very small function that only watches the `geofences` table and generates a notification in the case of any change to the table (insert/update/delete). From there, `pg_eventserv` pushing the notification out to any listening WebSockets clients. The clients are configured to reload the `geofences` layer when any changes occur, so they always display an up-to-date view of the `geofences`.

### FeatureServ Function

There is one function that is published by `pg_featureserv`, and allows the web clients to send it move requests to the database. This is how the clients cause the objects to move.

* `postgisftw.object_move()` takes in an object identifier and a direction, and moves the object in the specified direction by updating the current location in the `objects` table. All the rest of the system update actions are handled by the triggers attached to the `objects` table.

## Map Interface

There is a lot of content in the map interface, but most of it is around setting up the map and styling the fences and objects appropriately.

The actual movement and state changes are all handled in the `onmessage()`  method of the web socket.

* The method checks that the incoming message is structured as JSON, and parses it if it is.
* The method checks whether the message is an object state update or a geofence update.
* For geofences, if there has been a change it forces a refresh on the layer, which results in re-reading the layer from the server.
* For objects, it updates the object location, and if there has been a geofence enter/leave event, updates the color of the geofence to match the object.


