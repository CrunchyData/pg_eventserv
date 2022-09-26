# Notification Example

The simplest example, using only the [pg_eventserv](https://github.com/CrunchyData/pg_eventserv).

For an application that needs to reflect the state of the system in real-time, the UI might just subscribe to an event service that does nothing except let it know when records have been altered on the database. The UI can then proactively re-load them so the user is working with only the latest data.

## Tables

`application_data` just has a primary key and some data columns:

```sql
CREATE TABLE application_data (
  pk SERIAL PRIMARY KEY,
  name TEXT,
  value INTEGER
  );
```

## Functions

`change_notify()` is a trigger function that is attached to `application_data`. When an insert/update/delete occurs, it sends out a notification on the `changes` channel for any clients that are listening.

**Possible Enhancements:** The function could send out different notifications on different channels for different tables. The upside of this is only sending notifications to clients that care. The downside is that [pg_eventserv](https://github.com/CrunchyData/pg_eventserv) has to establish a distinct database connection for each channel (though not for each client: web socket clients all share the same database connection).

## HTML/JS

The user interface is just one big text area. The incoming payload is checked for JSON format in a try/catch block.

```js
try {
  var payload = JSON.parse(e.data);
  outputMessage.innerHTML = JSON.stringify(payload, null, 2);
  return;
}
catch (err) {
  outputMessage.innerHTML = e.data;
  return;
}
```


