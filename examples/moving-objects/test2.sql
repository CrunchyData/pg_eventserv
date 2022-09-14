
--
-- Very small test set of data that exercises the moving objects
-- schema and triggers. One fence, and objects that start within
-- and move without, triggering events.
--

DELETE FROM geofences;
INSERT INTO geofences (geog, label)
    VALUES (st_segmentize('POLYGON((-20 -20, 20 -20, 20 20, -20 20, -20 -20))'::geography,100000), 'square');

DELETE FROM objects;
INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(-30 30)', 1, Now());

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(30 30)', 2, Now());

UPDATE objects SET geog = ST_Translate(geog::geometry,
    CASE WHEN id = 1 THEN 2 ELSE -2 END * random(),
    -2 * random());


