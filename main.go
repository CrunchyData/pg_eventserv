package main

import (

	// Core
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	// Web Sockets Library
	"github.com/gorilla/websocket"

	// REST routing
	// "github.com/gorilla/handlers"
	// "github.com/gorilla/mux"

	// Send multiple channel messages at once
	// used to send notificaitons to web sockts
	"github.com/teivah/broadcast"

	// Configuration utilities
	"github.com/pborman/getopt/v2"
	"github.com/spf13/viper"

	// Logging
	log "github.com/sirupsen/logrus"

	// PostgreSQL connection
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4/pgxpool"
)

var pool *pgxpool.Pool

// programName is the name string we use
const programName string = "pg_eventserv"

// programVersion is the version string we use
const programVersion string = "0.1"

// var programVersion string
var globalSocketCount int = 0

// globalDb is a global database connection pointer
var globalDb *pgxpool.Pool = nil

var upgrader = websocket.Upgrader{
	ReadBufferSize:  0,
	WriteBufferSize: 0,
}

func init() {
	viper.SetDefault("DbConnection", "sslmode=disable")
	viper.SetDefault("HttpHost", "0.0.0.0")
	viper.SetDefault("HttpPort", 7700)
	viper.SetDefault("HttpsPort", 7701)
	viper.SetDefault("TlsServerCertificateFile", "")
	viper.SetDefault("TlsServerPrivateKeyFile", "")
	viper.SetDefault("UrlBase", "")
	viper.SetDefault("Debug", false)
	viper.SetDefault("AssetsPath", "./assets")
	// 1d, 1h, 1m, 1s, see https://golang.org/pkg/time/#ParseDuration
	viper.SetDefault("DbPoolMaxConnLifeTime", "1h")
	viper.SetDefault("DbPoolMaxConns", 4)
	viper.SetDefault("DbTimeout", 10)
	viper.SetDefault("CORSOrigins", []string{"*"})
	viper.SetDefault("BasePath", "/")
}

func main() {

	flagDebugOn := getopt.BoolLong("debug", 'd', "log debugging information")
	flagConfigFile := getopt.StringLong("config", 'c', "", "full path to config file", "config.toml")
	flagHelpOn := getopt.BoolLong("help", 'h', "display help output")
	flagVersionOn := getopt.BoolLong("version", 'v', "display version number")
	getopt.Parse()

	if *flagHelpOn {
		getopt.PrintUsage(os.Stdout)
		os.Exit(1)
	}

	if *flagVersionOn {
		fmt.Printf("%s %s\n", programName, programVersion)
		os.Exit(0)
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("es")

	// Commandline over-rides config file for debugging
	if *flagDebugOn {
		viper.Set("Debug", true)
		log.SetLevel(log.TraceLevel)
	}

	if *flagConfigFile != "" {
		viper.SetConfigFile(*flagConfigFile)
	} else {
		viper.SetConfigName(programName)
		viper.SetConfigType("toml")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/config")
		viper.AddConfigPath("/etc")
	}

	// Report our status
	log.Infof("%s %s", programName, programVersion)
	log.Info("Run with --help parameter for commandline options")

	// Read environment configuration first
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		viper.Set("DbConnection", dbURL)
		log.Info("Using database connection info from environment variable DATABASE_URL")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Debugf("viper.ConfigFileNotFoundError: %s", err)
		} else {
			if _, ok := err.(viper.UnsupportedConfigError); ok {
				log.Debugf("viper.UnsupportedConfigError: %s", err)
			} else {
				log.Fatalf("Configuration file error: %s", err)
			}
		}
	} else {
		// Really would like to log location of filename we found...
		// 	log.Infof("Reading configuration file %s", cf)
		if cf := viper.ConfigFileUsed(); cf != "" {
			log.Infof("Using config file: %s", cf)
		} else {
			log.Info("Config file: none found, using defaults")
		}
	}

	basePath := viper.GetString("BasePath")
	log.Infof("Serving HTTP  at %s/", formatBaseURL(fmt.Sprintf("http://%s:%d",
		viper.GetString("HttpHost"), viper.GetInt("HttpPort")), basePath))
	log.Infof("Serving HTTPS at %s/", formatBaseURL(fmt.Sprintf("http://%s:%d",
		viper.GetString("HttpHost"), viper.GetInt("HttpsPort")), basePath))

	// made a database connection pool
	db, err := dbConnect()
	if err != nil {
		log.Fatal("database connection failed")
		os.Exit(1)
	}

	// Create a relay for to pass Notifications through
	relay := broadcast.NewRelay[pgconn.Notification]()
	defer relay.Close()

	// Set up the listening thread
	go listenForNotify(db, "object", relay)

	// prepare the HTTP server object
	// more "production friendly" timeouts
	// https://blog.simon-frey.eu/go-as-in-golang-standard-net-http-config-will-break-your-production/#You_should_at_least_do_this_The_easy_path
	s := &http.Server{
		ReadTimeout:  1000, // 1 * time.Second,
		WriteTimeout: 1000, // writeTimeout,
		Addr:         fmt.Sprintf("%s:%d", viper.GetString("HttpHost"), viper.GetInt("HttpPort")),
		Handler:      webSocketHandler(relay),
	}

	// start http service
	go func() {
		// ListenAndServe returns http.ErrServerClosed when the server receives
		// a call to Shutdown(). Other errors are unexpected.
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Temporarily disable origin checking on the websockets upgrader
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	// wait here for interrupt signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	//http.HandleFunc("/object", webSocketHandler(relay.Listener(1)))

	// http.HandleFunc("/objectold", func(w http.ResponseWriter, r *http.Request) {
	// 	ws, _ := upgrader.Upgrade(w, r, nil) // error ignored for sake of simplicity

}

// goroutine to watch a PgSQL LISTEN/NOTIFY channel, and
// send the notification object to the broadcast relay
// when an event comes through
func listenForNotify(db *pgxpool.Pool, listenChannel string, relay *broadcast.Relay[pgconn.Notification]) {
	// draw a connection from the pool to use for listening
	conn, err := db.Acquire(context.Background())
	if err != nil {
		log.Fatalf("Error acquiring connection: %s", err)
	}
	defer conn.Release()
	log.Infof("Listening to the '%s' database channel\n", listenChannel)

	// send the LISTEN command to the connection
	_, err = conn.Exec(context.Background(), fmt.Sprintf("LISTEN %s", listenChannel))
	if err != nil {
		log.Fatalf("Error listening to '%s' channel: %s", listenChannel, err)
	}

	// wait for notifications to come off the connection
	for {
		notification, err := conn.Conn().WaitForNotification(context.Background())
		if err != nil {
			log.Warnf("Error from WaitForNotification():", err)
		}

		log.Debugf("NOTIFY received, channel '%s', payload '%s'",
			notification.Channel,
			notification.Payload)

		// send the notification to all listeners connected to the relay
		relay.NotifyCtx(context.Background(), *notification)
	}
}

// generator function to create http.Handler to the http.Server
// to run. Handler converts request to websocket, and sets up
// goroutine to listen for broadcast messages from the db notification
// goroutine. Websocket is held open by pinging client regularly.
func webSocketHandler(relay *broadcast.Relay[pgconn.Notification]) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// keep a unique number for each new socket we create
		globalSocketCount = globalSocketCount + 1
		iSocket := globalSocketCount
		log.Debugf("handling web socket creation request")
		// create the web socket
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warnf("web socket creation failed: err")
			return
		}
		log.Debugf("created websocket %d", iSocket)
		defer ws.Close()

		// Create a listener for the relay
		lst := relay.Listener(1)

		// Gorouting to monitor the relay listener and send messages
		// to the web socket client when new notifications arrive
		go func() {
			for n := range lst.Ch() { // Ranges over notifications
				log.Debugf("sending notification to web socket %d: %s", iSocket, n.Payload)
				bPayload := []byte(n.Payload)
				if err := ws.WriteMessage(websocket.TextMessage, bPayload); err != nil {
					log.Debugf("web socket %d closed connection", iSocket)
					lst.Close()
					return
				}
			}
		}()

		// Keep the web socket open as long as the client keeps
		// its side open.
		for {
			wserr := ws.WriteMessage(websocket.PingMessage, []byte("ping"))
			// When socket no longer accepts writes, close it and the associated
			// listener
			if wserr != nil {
				log.Infof("closing web socket %d after write failure", iSocket)
				lst.Close()
				return
			}
			// Only ping the client every few seconds
			time.Sleep(time.Second * 2)
		}

	})
}

// func getRouter() *mux.Router {
// 	// creates a new instance of a mux router
// 	r := mux.NewRouter().
// 		StrictSlash(true).
// 		PathPrefix(
// 			"/" +
// 				strings.TrimLeft(viper.GetString("BasePath"), "/"),
// 		).
// 		Subrouter()

// 	// Front page and layer list
// 	r.Handle("/", http.HandlerFunc(requestHomeHTML))
// 	r.Handle("/index.html", http.HandlerFunc(requestHomeHTML))
// 	// r.Handle("/index.json", tileAppHandler(requestListJSON))
// 	// Tile requests
// 	// r.Handle("/{name}/{z:[0-9]+}/{x:[0-9]+}/{y:[0-9]+}.{ext}", tileMetrics(tileAppHandler(requestTiles)))

// 	return r
// }

// func requestHomeHTML(w http.ResponseWriter, r *http.Request) error {
// 	log.WithFields(log.Fields{
// 		"event": "request",
// 		"topic": "root",
// 	}).Trace("requestListHtml")

// 	content, err := ioutil.ReadFile(fmt.Sprintf("%s/index.html", viper.GetString("AssetsPath")))
// 	if err != nil {
// 		return err
// 	}

// 	w.Write(content)

// 	return nil
// }
