
DROP SCHEMA IF EXISTS chat CASCADE;
CREATE SCHEMA chat;
SET search_path = chat,public;

-- store the users and channels in normalized
-- form like good little database designers!
DROP TABLE IF EXISTS chat.users;
CREATE TABLE chat.users (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  created timestamptz NOT NULL DEFAULT Now(),
  latest timestamptz NOT NULL DEFAULT Now()
);

-- we want unique user names!
CREATE UNIQUE INDEX users_name_x
  ON chat.users(name);

DROP TABLE IF EXISTS chat.channels;
CREATE TABLE chat.channels (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  created timestamptz NOT NULL DEFAULT Now(),
  latest timestamptz NOT NULL DEFAULT Now()
);

-- we want unique channel names!
CREATE UNIQUE INDEX channels_name_x
  ON chat.channels(name);

-- Since channel and user names are normalized
-- we declare them as foreign key constraints
DROP TABLE IF EXISTS chat.messages;
CREATE TABLE chat.messages (
  id SERIAL PRIMARY KEY,
  channel_id BIGINT REFERENCES chat.channels(id),
  user_id BIGINT REFERENCES chat.users(id),
  ts TIMESTAMPTZ NOT NULL DEFAULT Now(),
  message TEXT
);

-- Future use: searching the contents of the
-- chat messages using full text search!
CREATE INDEX messages_message_tsx
  ON chat.messages
  USING GIST (to_tsvector('english', message));

-- Might want to search using time stamp
-- to get a particular slice of chats.
CREATE INDEX messages_ts_x
  ON chat.messages (ts);

CREATE SCHEMA IF NOT EXISTS postgisftw;

-- We need one external function (at least) to
-- send in new messages to the system. Other functions
-- we might want to add could list the existing
-- channel and users.
CREATE OR REPLACE FUNCTION postgisftw.message_send(
	username text, channel text, message text)
RETURNS TABLE(user_id bigint, channel_id bigint, message_id bigint, ts timestamptz)
AS $$
DECLARE
  user_id bigint;
  channel_id bigint;
  message_id bigint;
BEGIN

  -- Normalize the user and channel names to lower case
  username := lower(username);
  channel := lower(channel);

  -- Read the user id if it already exists, or add it
  -- if it does not
  INSERT INTO chat.users (name) VALUES (username)
      ON CONFLICT (name) DO UPDATE SET latest = Now()
      RETURNING id INTO user_id;

  -- Read the channel id if it already exists, or add it
  -- if it does not
  INSERT INTO chat.channels (name) VALUES (channel)
      ON CONFLICT (name) DO UPDATE SET latest = Now()
      RETURNING id INTO channel_id;

  -- Add the message into the message log table
  INSERT INTO chat.messages (user_id, channel_id, message)
    VALUES(user_id, channel_id, message)
    RETURNING id INTO message_id;

  -- Return the ids to the client in case it wants
  -- to maintain a local buffer of messages, etc.
  RETURN QUERY SELECT
    user_id AS user_id,
    channel_id AS channel_id,
    message_id AS message_id,
    Now() AS ts;

END;
$$
LANGUAGE 'plpgsql' VOLATILE;

-- When a new message hits the message log
-- we want to trigger a message on the listen/notify
-- bus so all the connected clients see the message.
-- Mostly this is just bundling up everything
-- we know into a JSON payload and sending it
-- off over the bus.
DROP FUNCTION IF EXISTS message_broadcast CASCADE;
CREATE FUNCTION message_broadcast() RETURNS trigger AS $$
    DECLARE
        broadcast_json jsonb;
    BEGIN
        SELECT
        json_build_object(
          'user_name', users.name,
          'user_id', users.id,
          'channel_name', channels.name,
          'channel_id', channels.id,
          'message_id', messages.id,
          'message', messages.message,
          'ts', messages.ts
        ) INTO broadcast_json
        FROM chat.messages
        JOIN chat.users ON (messages.user_id = users.id)
        JOIN chat.channels ON (messages.channel_id = channels.id)
        WHERE messages.id = NEW.id;

        RAISE DEBUG 'broadcast_json %', broadcast_json;

        -- Send the update out on the channel
        PERFORM (
            SELECT pg_notify(
              broadcast_json->>'channel_name',
              broadcast_json::text)
        );

        RETURN NEW;
    END;
$$ LANGUAGE 'plpgsql';

-- We'll only do broadcasts for new messages. If we had a UI
-- that supported editing, we could also broadcast on update and
-- delete.
DROP TRIGGER IF EXISTS message_broadcast ON chat.messages;
CREATE TRIGGER message_broadcast
    AFTER INSERT ON chat.messages
    FOR EACH ROW
        EXECUTE FUNCTION message_broadcast();
