
-- A table full of data used by an application,
-- any table, any data. Primary key is always
-- useful, but everything is quite application
-- dependent.
CREATE TABLE application_data (
  pk SERIAL PRIMARY KEY,
  name TEXT,
  value INTEGER
  );

-- When a change hits the table we want to client
-- to know about it so it can update the data. It
-- can read the new data from this packet, or just
-- ignore it and re-pull the data using the primary
-- key.
DROP FUNCTION IF EXISTS change_notify CASCADE;
CREATE OR REPLACE FUNCTION change_notify() RETURNS trigger AS $$
    DECLARE
        notify_json jsonb;
    BEGIN
        SELECT
        jsonb_build_object(
          'table_name', TG_TABLE_NAME::text,
          'primary_key', NEW.pk,
          'change_type', TG_OP::text
          -- ,'data', row_to_json(NEW.*)
        ) INTO notify_json;

        PERFORM (
            SELECT pg_notify(
              'changes',
              notify_json::text)
        );

        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

-- Set the notification for all changes, so the UI
-- can keep in synch with the server.
DROP TRIGGER IF EXISTS change_notify ON application_data;
CREATE TRIGGER change_notify
    AFTER INSERT OR UPDATE OR DELETE ON application_data
    FOR EACH ROW
        EXECUTE FUNCTION change_notify();
