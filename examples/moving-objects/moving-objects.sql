
CREATE EXTENSION IF NOT EXISTS intarray;
CREATE EXTENSION IF NOT EXISTS postgis;

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
-- FUNCTIONS
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

        -- Ensure geofence state gets saved
        NEW.fences := fences_new;
        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

DROP TRIGGER IF EXISTS objects_geofence ON moving.objects;
CREATE TRIGGER objects_geofence
    AFTER INSERT OR UPDATE ON moving.objects
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
        fences_old integer[];
        fences_entered integer[];
        fences_left integer[];
        channel text := 'objects';
        events_json jsonb;
        location_json jsonb;
        payload_json jsonb;
    BEGIN
        -- Place a copy of the value into the history table
        INSERT INTO moving.objects_history (id, geog, ts, props)
            VALUES (NEW.id, NEW.geog, NEW.ts, NEW.props);

        -- Clean up any nulls
        fences_old := coalesce(OLD.fences, ARRAY[]::integer[]);

        -- Compare to previous fences state
        fences_entered = NEW.fences - fences_old;
        fences_left = fences_old - NEW.fences;

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
                'latitude', ST_Y(NEW.geog::geometry),
                'ts', NEW.ts,
                'props', NEW.props))
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
    BEFORE INSERT OR UPDATE ON moving.objects
    FOR EACH ROW
        EXECUTE FUNCTION objects_update();

------------------------------------------------------------------------
