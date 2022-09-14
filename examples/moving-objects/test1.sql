
--
-- Very small test set of data that exercises the moving objects
-- schema and triggers. One fence, and objects that start within
-- and move without, triggering events.
--

INSERT INTO geofences (geog, label)
    VALUES ('POLYGON((-1 -1, 1 -1, 1 1, -1 1, -1 -1))', 'square');

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(0 0)', 1, Now());

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(0 0.5)', 1, Now())
    ON CONFLICT (id) DO UPDATE SET geog = EXCLUDED.geog, ts = EXCLUDED.ts;

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(1 1)', 2, Now());

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(3 2)', 1, Now())
    ON CONFLICT (id) DO UPDATE SET geog = EXCLUDED.geog, ts = EXCLUDED.ts;

INSERT INTO objects (geog, id, ts)
    VALUES ('POINT(0 0)', 1, Now())
    ON CONFLICT (id) DO UPDATE SET geog = EXCLUDED.geog, ts = EXCLUDED.ts;

