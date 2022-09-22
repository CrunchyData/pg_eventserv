--
-- We depend on intarray to handle the setwise logic
-- of determinig if the current fences a object is within
-- is different from the ones it used to be in, to catch
-- changes in state.
--
CREATE EXTENSION IF NOT EXISTS intarray;

-- We depend on postgis for obvious reasons, doing the
-- geofence calculation and any other geostuff.
CREATE EXTENSION IF NOT EXISTS postgis;

-- Create all these functions in a separate schema, for
-- cleanliness.
CREATE SCHEMA IF NOT EXISTS moving;
SET search_path = moving,public;

------------------------------------------------------------------------
-- TABLES
------------------------------------------------------------------------

--
-- The objects table contains the current state of the object
-- set: where they are, and what fences they are currently in.
-- It is unlogged for speed, but might still suffer bload and
-- need regular proactive vacuuming. Research project here.
--
DROP TABLE IF EXISTS objects;
CREATE UNLOGGED TABLE objects (
    id integer PRIMARY KEY,
    geog geography(Point),
    ts timestamptz DEFAULT now(),
    color text,
    props json,
    fences integer[]
);

CREATE SEQUENCE objects_id_seq OWNED BY objects.id;
ALTER TABLE objects ALTER COLUMN id SET DEFAULT nextval('objects_id_seq');
CREATE UNIQUE INDEX objects_id_x ON objects (id);
CREATE INDEX objects_geog_x ON objects USING GIST (geog);

--
-- The objects_history table contains every record we are sent
-- in by the objects. We can use this to generate retrospective paths
-- for display and analysis.
--
DROP TABLE IF EXISTS objects_history;
CREATE TABLE objects_history (
    history_id integer PRIMARY KEY,
    id integer,
    geog geography(Point),
    ts timestamptz,
    props json
);

CREATE SEQUENCE objects_history_id_seq OWNED BY objects_history.history_id;
ALTER TABLE objects_history ALTER COLUMN history_id SET DEFAULT nextval('objects_history_id_seq');
CREATE INDEX objects_history_id_x ON objects_history (id);
CREATE INDEX objects_history_ts_x ON objects (ts);
CREATE INDEX objects_history_geog_x ON objects USING GIST (geog);

--
-- The geofences table holds the polygons to be used to generate
-- "entrance" and "exit" events for objects.
--
DROP TABLE IF EXISTS geofences;
CREATE TABLE geofences (
    id integer PRIMARY KEY,
    geog geography(Polygon),
    label text,
    ts timestamptz DEFAULT now()
);

CREATE SEQUENCE geofences_id_seq OWNED BY geofences.id;
ALTER TABLE geofences ALTER COLUMN id SET DEFAULT nextval('geofences_id_seq');
CREATE INDEX geofences_id_x ON geofences (id);
CREATE INDEX geofences_geog_x ON geofences USING GIST (geog);

------------------------------------------------------------------------
-- OBJECT STATE FUNCTIONS
------------------------------------------------------------------------

--
-- objects_geofence() is a trigger function that is run BEFORE
-- the row is actually submitted to the table for storage.
-- We calculate what geofences the current state of the object
-- is crossing, so that in the next step it can be compared to
-- the last known set of geofences.
--
DROP FUNCTION IF EXISTS objects_geofence CASCADE;
CREATE FUNCTION objects_geofence() RETURNS trigger AS $$
    DECLARE
        fences_new integer[];
    BEGIN
        -- Add the current geofence state to the input
        -- tuple every time.
        SELECT coalesce(array_agg(id), ARRAY[]::integer[])
            INTO fences_new
            FROM moving.geofences
            WHERE ST_Intersects(geofences.geog, new.geog);

        RAISE DEBUG 'fences_new %', fences_new;
        -- Ensure geofence state gets saved
        NEW.fences := fences_new;
        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS objects_geofence ON moving.objects;
CREATE TRIGGER objects_geofence
    BEFORE INSERT OR UPDATE ON moving.objects
    FOR EACH ROW
        EXECUTE FUNCTION objects_geofence();

--
-- objects_update() is a trigger function run AFTER
-- the row is submitted to the table. So if the row
-- cannot be inserted (for example, breaks the unique
-- id constraint) then this function is not run.
-- If the row can be stored in the objects table, then
-- we also want to memorialize it in the history table,
-- and build up a notification payload to feed out to
-- any clients watching the listen/notify channel.
--
DROP FUNCTION IF EXISTS objects_update CASCADE;
CREATE FUNCTION objects_update() RETURNS trigger AS $$
    DECLARE
        channel text := 'objects';
        fences_old integer[];
        fences_entered integer[];
        fences_left integer[];
        events_json jsonb;
        location_json jsonb;
        payload_json jsonb;
    BEGIN
        -- Place a copy of the value into the history table
        INSERT INTO moving.objects_history (id, geog, ts, props)
            VALUES (NEW.id, NEW.geog, NEW.ts, NEW.props);

        -- Clean up any nulls
        fences_old := coalesce(OLD.fences, ARRAY[]::integer[]);
        RAISE DEBUG 'fences_old %', fences_old;

        -- Compare to previous fences state
        fences_entered = NEW.fences - fences_old;
        fences_left = fences_old - NEW.fences;

        RAISE DEBUG 'fences_entered %', fences_entered;
        RAISE DEBUG 'fences_left %', fences_left;

        -- Form geofence events into JSON for notify payload
        WITH r AS (
        SELECT 'entered' AS action,
            g.id AS geofence_id,
            g.label AS geofence_label
        FROM moving.geofences g
        WHERE g.id = ANY(fences_entered)
        UNION
        SELECT 'left' AS action,
            g.id AS geofence_id,
            g.label AS geofence_label
        FROM moving.geofences g
        WHERE g.id = ANY(fences_left)
        )
        SELECT json_agg(row_to_json(r))
        INTO events_json
        FROM r;

        -- Form notify payload
        SELECT json_build_object(
            'object_id', NEW.id,
            'events', events_json,
            'location', json_build_object(
                'longitude', ST_X(NEW.geog::geometry),
                'latitude', ST_Y(NEW.geog::geometry)),
            'ts', NEW.ts,
            'color', NEW.color,
            'props', NEW.props)
        INTO payload_json;

        RAISE DEBUG '%', payload_json;

        -- Send the payload out on the channel
        PERFORM (
            SELECT pg_notify(channel, payload_json::text)
        );

        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS objects_update ON moving.objects;
CREATE TRIGGER objects_update
    AFTER INSERT OR UPDATE ON moving.objects
    FOR EACH ROW
        EXECUTE FUNCTION objects_update();


------------------------------------------------------------------------
--
-- In case we want the UI to update the geofence layer when
-- new fences are added to the database, we just send a simple
-- update payload here.
--

DROP FUNCTION IF EXISTS layer_change CASCADE;
CREATE FUNCTION layer_change() RETURNS trigger AS $$
    DECLARE
        layer_change_json json;
        channel text := 'objects';
    BEGIN
        -- Tell the client what layer changed and how
        SELECT json_build_object(
            'layer', TG_TABLE_NAME::text,
            'change', TG_OP)
          INTO layer_change_json;

        RAISE DEBUG 'layer_change %', layer_change_json;
        PERFORM (
            SELECT pg_notify(channel, layer_change_json::text)
        );
        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS layer_change ON moving.geofences;
CREATE TRIGGER layer_change
    BEFORE INSERT OR UPDATE OR DELETE ON moving.geofences
    FOR EACH STATEMENT
        EXECUTE FUNCTION layer_change();


------------------------------------------------------------------------
--
-- Very small test set of data that exercises the moving objects
-- schema and triggers. One fence, and objects that start within
-- and move without, triggering events.
--

INSERT INTO moving.geofences (geog, label)
    VALUES (ST_Segmentize('POLYGON((-10 -10, 10 -10, 10 10, -10 10, -10 -10))'::geography, 100000), 'square');

INSERT INTO moving.geofences (geog, label)
    VALUES (ST_Buffer('POINT(-110 40)'::geography, 1000000), 'circle');

INSERT INTO moving.objects (geog, id, color)
    VALUES ('POINT(15 0)', 1, 'red');

INSERT INTO moving.objects (geog, id, color)
    VALUES ('POINT(-15 0)', 2, 'green');

INSERT INTO moving.objects (geog, id, color)
    VALUES ('POINT(-90 30)', 3, 'purple');


------------------------------------------------------------------------
--
-- Expose a function via pg_featureserv to hook the UI up/down/left/right
-- buttons up to the object state.
--

CREATE SCHEMA IF NOT EXISTS postgisftw;
CREATE OR REPLACE FUNCTION postgisftw.object_move(
	move_id integer, direction text)
RETURNS TABLE(id integer, geog geography)
AS $$
DECLARE
  xoff real = 0.0;
  yoff real = 0.0;
  step real = 2.0;
BEGIN

  yoff := CASE
    WHEN direction = 'up' THEN 1 * step
    WHEN direction = 'down' THEN -1 * step
    ELSE 0.0 END;

  xoff := CASE
    WHEN direction = 'left' THEN -1 * step
    WHEN direction = 'right' THEN 1 * step
    ELSE 0.0 END;

  RETURN QUERY UPDATE moving.objects mo
    SET geog = ST_Translate(mo.geog::geometry, xoff, yoff)::geography
    WHERE mo.id = move_id
    RETURNING mo.id, mo.geog;

END;
$$
LANGUAGE 'plpgsql' VOLATILE;
