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


## Trying It Out

All the code and files are available in this [examples directory](https://github.com/CrunchyData/pg_eventserv/tree/main/examples/moving-objects).

* You can run the database in [Crunchy Bridge](https://crunchybridge.com/) and the services using [Container Apps](https://docs.crunchybridge.com/container-apps/) in Crunchy Bridge.
* Or you can run the database on your local computer and also run the services locally using an appropriate binary for your operating system.

### Load Database

First, load up the [moving-objects.sql](moving-objects.sql) file in your database. This creates all the tables and triggers to keep the model up-to-date. A good practice is to use an `application` user instead of the `postgres` user as the owner for the tables and triggers. That way you can connect the services using a lower-priv database user.

### Start Services (Container Apps)

Now, start up the services as [container apps](https://docs.crunchybridge.com/container-apps/)! (This you will have to do as the `postgres` user.)

This command starts up `pg_featureserv`:

```
SELECT run_container('
    -dt
    -p 5437:9000/tcp
    --log-driver k8s-file
    --log-opt max-size=1mb
    -e DATABASE_URL="postgres://application:xxxxxxxxxx@p.xxxxxxxxxx.db.postgresbridge.com:5432/dbname"
    -e PGFS_SERVER_HTTPPORT=9000
    -e PGFS_PAGING_LIMITDEFAULT=10000
    -e PGFS_PAGING_LIMITMAX=10000
    docker.io/pramsey/pg_featureserv:latest
    ');
```

The newlines in the example above have to be stripped out before running the SQL command. Note that the external port is **5437** in order to be within the allowable port range for container apps. This is important below when hooking up the web UI to the services.

This command starts up `pg_eventserv`:

```
SELECT run_container('
    -dt
    -p 5438:7700/tcp
    --log-driver k8s-file
    --log-opt max-size=1mb
    -e DATABASE_URL="postgres://application:xxxxxxxx@p.xxxxxxxx.db.postgresbridge.com:5432/dbname"
    -e ES_HTTPPORT=7700
    docker.io/pramsey/pg_eventserv:latest
');
```

Now you should be up and running! You can also just download the binaries of the two services directly and run them locally and use the default `localhost` addresses.

### Start Services (Local Apps)

If you have downloaded [pg_featureserv](https://github.com/crunchydata/pg_featureserv) and [pg_eventserv](https://github.com/crunchydata/pg_eventserv) binaries, then firing up the services is very easy!

Unzip the feature service download and start it up!

```bash
mkdir pg_featureserv
cd pg_featureserv
unzip ../pg_featureserv_latest_linux.zip
export DATABASE_URL=postgres://dbuser:dbpass@dbhost:5432/dbname
./pg_featureserv
```

Unzip the event service download and start it up!

```bash
mkdir pg_eventserv
cd pg_eventserv
unzip ../pg_eventserv_latest_linux.zip
export DATABASE_URL=postgres://dbuser:dbpass@dbhost:5432/dbname
./pg_eventserv
```


### Modify Map Client

The [map client JavaScript](moving-objects.js) needs to be modified to point at your new servers, on their ports!

Find the following variables and edit them to point to your services.

The event server web socket host:

```
var wsHost = "ws://yourhostname:7700";
```

Note that the port is not the default service port, but the port the container is running at on Crunchy Bridge.

The feature server HTTP host:

```
var fsHost = "http://yourhostname:9000";
```

If you are running the services locally, you can just leave the example code as is.


### Try it!

Open up the [HTML page](moving-objects.html), and you should see the working map!

