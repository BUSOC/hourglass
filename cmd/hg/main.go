package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/busoc/hourglass"
	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/midbel/cli"
	"github.com/midbel/jwt"
	"github.com/midbel/rustine"
)

const MaxBodySize = 1 << 32

var db *sql.DB

type T struct {
	Secret string `json:"secret"`
	Issuer string `json:"issuer"`
	TTL    int    `json:"ttl"`
}

type I struct {
	Prefix string   `json:"prefix"`
	User   string   `json:"user"`
	Passwd string   `json:"passwd"`
	Hosts  []string `json:"sources"`
}

type Config struct {
	Addr     string `json:"addr"`
	Database string `json:"db"`
	Token    *T     `json:"token"`
	Import   *I     `json:"import"`
}

func init() {
	cli.Version = "1.1-beta"
	cli.BuildTime = "2018-08-03 06:07:00"
}

func main() {
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *version {
		log.SetFlags(0)
		log.Printf("%s version %s %s", filepath.Base(os.Args[0]), cli.Version, cli.BuildTime)
		os.Exit(1)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatalln(err)
	}

	var c Config
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		log.Fatalln(err)
	}
	f.Close()

	if c, err := sql.Open("postgres", c.Database); err != nil {
		log.Fatalln(err)
	} else {
		db = c
	}
	if err := db.Ping(); err != nil {
		log.Fatalln(err)
	}
	defer db.Close()

	r := mux.NewRouter()
	if err := setupRoutes(r, &c); err != nil {
		log.Fatalln(err)
	}
	if c.Import != nil && c.Import.Prefix != "" {
		setupRoutesBis(r, &c)
	}
	if err := http.ListenAndServe(c.Addr, r); err != nil {
		log.Fatalln(err)
	}
}

func setupRoutesBis(r *mux.Router, c *Config) error {
	c.Import.Prefix = strings.Trim(c.Import.Prefix, "/")

	paths := []struct {
		Path    string
		Method  string
		Handler Func
	}{
		{Path: "/events/", Handler: listEvents},
		{Path: "/events/", Handler: newEvent, Method: http.MethodPost},
		{Path: "/events/{id:[0-9]+}", Handler: viewEvent},
		{Path: "/events/{id:[0-9]+}", Handler: newEvent, Method: http.MethodPost},
		{Path: "/events/{id:[0-9]+}", Handler: updateEvent, Method: http.MethodPut},
		{Path: "/events/{id:[0-9]+}", Handler: deleteEvent, Method: http.MethodDelete},
		{Path: "/events/{source:[a-zA-Z0-9_]+}/", Handler: importEvents, Method: http.MethodPost},
		{Path: "/sources/", Handler: listSources},
		{Path: "/users/", Handler: listUsers},
		{Path: "/categories/", Handler: listCategories},
		{Path: "/dors/", Handler: listJournals},
		{Path: "/events/", Handler: listEvents},
		{Path: "/todos/", Handler: listTodos},
		{Path: "/files/", Handler: listFiles},
		{Path: "/slots/", Handler: listSlots},
		{Path: "/uplinks/", Handler: listUplinks},
		{Path: "/downlinks/", Handler: listDownlinks},
		{Path: "/transfers/", Handler: listTransfers},
		{Path: "/users/{id:[0-9]+}", Handler: viewUser},
		{Path: "/categories/{id:[0-9]+}", Handler: viewCategory},
		{Path: "/dors/{id:[0-9]+}", Handler: viewJournal},
		{Path: "/events/{id:[0-9]+}", Handler: viewEvent},
		{Path: "/todos/{id:[0-9]+}", Handler: viewTodo},
		{Path: "/files/{id:[0-9]+}", Handler: viewFile},
		{Path: "/slots/{id:[0-9]+}", Handler: viewSlot},
		{Path: "/uplinks/{id:[0-9]+}", Handler: viewUplink},
		{Path: "/downlinks/{id:[0-9]+}", Handler: viewDownlink},
		{Path: "/transfers/{id:[0-9]+}", Handler: viewTransfer},
	}
	s := r.PathPrefix("/" + c.Import.Prefix).Subrouter()
	for _, p := range paths {
		h := allow(p.Handler, os.Stderr, c.Import.User, c.Import.Passwd, c.Import.Hosts)
		if p.Method == "" {
			p.Method = http.MethodGet
		}
		s.Handle(p.Path, h).Methods(p.Method, http.MethodOptions)
	}

	return nil
}

func setupRoutes(r *mux.Router, c *Config) error {
	if c.Token.Secret == "random" || c.Token.Secret == "" {
		c.Token.Secret = rustine.RandomString(16)
	}
	options := []jwt.Option{
		jwt.WithSecret([]byte(c.Token.Secret), jwt.HS512),
		jwt.WithIssuer(c.Token.Issuer),
		jwt.WithTime(time.Duration(c.Token.TTL)*time.Second, 0),
	}
	s, err := jwt.New(options...)
	// s, err := jwt.New("hs256", c.Token.Secret, c.Token.Issuer, c.Token.TTL)
	if err != nil {
		return err
	}
	r.Handle("/auth", signin(s)).Methods("POST", "OPTIONS")

	r.Handle("/users/", handle(listUsers, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/users/", handle(registerUser, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/users/{id:[0-9]+}", handle(viewUser, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/users/{id:[0-9]+}", handle(updateUser, os.Stderr, s)).Methods("PUT", "OPTIONS")
	r.Handle("/users/{id:[0-9]+}/passwd", handle(updatePasswd, os.Stderr, s)).Methods("PUT", "OPTIONS")

	r.Handle("/categories/", handle(listCategories, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/categories/", handle(newCategory, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/categories/{id:[0-9]+}", handle(viewCategory, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/categories/{id:[0-9]+}", handle(newCategory, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/categories/{id:[0-9]+}", handle(updateCategory, os.Stderr, s)).Methods("PUT", "OPTIONS")

	r.Handle("/dors/", handle(listJournals, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/dors/", handle(newJournal, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/dors/{id:[0-9]+}", handle(viewJournal, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/dors/{id:[0-9]+}", handle(updateJournal, os.Stderr, s)).Methods("PUT", "OPTIONS")
	r.Handle("/dors/{id:[0-9]+}", handle(deleteJournal, os.Stderr, s)).Methods("DELETE", "OPTIONS")

	r.Handle("/sources/", handle(listSources, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/events/", handle(listEvents, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/events/", handle(newEvent, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/events/{id:[0-9]+}", handle(viewEvent, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/events/{id:[0-9]+}", handle(newEvent, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/events/{id:[0-9]+}", handle(updateEvent, os.Stderr, s)).Methods("PUT", "OPTIONS")
	r.Handle("/events/{id:[0-9]+}", handle(deleteEvent, os.Stderr, s)).Methods("DELETE", "OPTIONS")

	r.Handle("/todos/", handle(listTodos, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/todos/", handle(newTodo, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/todos/{id:[0-9]+}", handle(viewTodo, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/todos/{id:[0-9]+}", handle(newTodo, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/todos/{id:[0-9]+}", handle(updateTodo, os.Stderr, s)).Methods("PUT", "OPTIONS")
	r.Handle("/todos/{id:[0-9]+}", handle(deleteTodo, os.Stderr, s)).Methods("DELETE", "OPTIONS")

	r.Handle("/files/", handle(listFiles, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/files/", handle(newFile, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/files/{id:[0-9]+}", handle(viewFile, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/files/{id:[0-9]+}", handle(newFile, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/files/{id:[0-9]+}", handle(updateFile, os.Stderr, s)).Methods("PUT", "OPTIONS")
	r.Handle("/files/{id:[0-9]+}", handle(deleteFile, os.Stderr, s)).Methods("DELETE", "OPTIONS")

	r.Handle("/slots/", handle(listSlots, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/slots/", handle(newSlot, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/slots/{id:[0-9]+}", handle(viewSlot, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/slots/{id:[0-9]+}", handle(deleteSlot, os.Stderr, s)).Methods("DELETE", "OPTIONS")

	r.Handle("/uplinks/", handle(listUplinks, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/uplinks/", handle(newUplink, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/uplinks/{id:[0-9]+}", handle(viewUplink, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/uplinks/{id:[0-9]+}", handle(updateUplink, os.Stderr, s)).Methods("PUT", "OPTIONS")

	r.Handle("/downlinks/", handle(listDownlinks, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/downlinks/", handle(newDownlink, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/downlinks/{id:[0-9]+}", handle(viewDownlink, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/downlinks/{id:[0-9]+}", handle(updateDownlink, os.Stderr, s)).Methods("PUT", "OPTIONS")

	r.Handle("/transfers/", handle(listTransfers, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/transfers/", handle(newTransfer, os.Stderr, s)).Methods("POST", "OPTIONS")
	r.Handle("/transfers/{id:[0-9]+}", handle(viewTransfer, os.Stderr, s)).Methods("GET", "OPTIONS")
	r.Handle("/transfers/{id:[0-9]+}", handle(updateTransfer, os.Stderr, s)).Methods("PUT", "OPTIONS")

	return nil
}

func updateCategory(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	s, err := hourglass.ViewCategory(db, id)
	if err != nil {
		return nil, err
	}
	c := &hourglass.Category{Name: s.Name}
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(c); err != nil {
		return nil, err
	}
	c.Id = id
	c.User = r.Context().Value("user").(string)
	if err := hourglass.UpdateCategory(db, c); err != nil {
		return nil, err
	}
	return c, nil
}

func updateUplink(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	var s string
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&s); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.UpdateUplink(db, id, s, u)
}

func updateDownlink(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	var s string
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&s); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.UpdateDownlink(db, id, s, u)
}

func updateTransfer(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	var s string
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&s); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.UpdateTransfer(db, id, s, u)
}

func deleteSlot(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	s, err := hourglass.ViewSlot(db, id)
	if err != nil {
		return nil, err
	}
	s.User = r.Context().Value("user").(string)
	return nil, hourglass.DeleteSlot(db, s)
}

func newSlot(r *http.Request) (interface{}, error) {
	s := new(hourglass.Slot)
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(s); err != nil {
		return nil, err
	}
	s.User = r.Context().Value("user").(string)
	return s, hourglass.NewSlot(db, s)
}

func newUplink(r *http.Request) (interface{}, error) {
	v := struct {
		S int `json:"slot"`
		E int `json:"event"`
		F int `json:"file"`
	}{}
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&v); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.NewUplink(db, v.S, v.E, v.F, u)
}

func newTransfer(r *http.Request) (interface{}, error) {
	v := struct {
		U int    `json:"uplink"`
		E int    `json:"event"`
		L string `json:"location"`
	}{}
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&v); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.NewTransfer(db, v.U, v.E, u, v.L)
}

func newCategory(r *http.Request) (interface{}, error) {
	c := new(hourglass.Category)
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(c); err != nil {
		return nil, err
	}
	c.Id, _ = strconv.Atoi(mux.Vars(r)["id"])
	c.User = r.Context().Value("user").(string)
	return c, hourglass.NewCategory(db, c)
}

func newDownlink(r *http.Request) (interface{}, error) {
	v := struct {
		Event int `json:"event"`
		Slot  int `json:"slot"`
		File  int `json:"file"`
	}{}
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxBodySize)).Decode(&v); err != nil {
		return nil, err
	}
	u := r.Context().Value("user").(string)
	return hourglass.NewDownlink(db, v.Event, v.Slot, v.File, u)
}

func listSlots(r *http.Request) (interface{}, error) {
	q := r.URL.Query()
	return hourglass.ListSlots(db, q["category[]"])
}

func listCategories(r *http.Request) (interface{}, error) {
	return hourglass.ListCategories(db)
}

func listDownlinks(r *http.Request) (interface{}, error) {
	var fd, td time.Time
	q := r.URL.Query()
	if f, t := q.Get("dtstart"), q.Get("dtend"); len(f) > 0 && len(t) > 0 {
		var err error
		if fd, err = time.Parse(time.RFC3339, f); err != nil {
			return nil, err
		}
		if td, err = time.Parse(time.RFC3339, t); err != nil {
			return nil, err
		}
	} else {
		fd = time.Now().Truncate(time.Hour * 24)
		td = fd.Add(time.Hour * 24)
	}
	return hourglass.ListDownlinks(db, fd, td, q["category[]"], q["status[]"])
}

func listUplinks(r *http.Request) (interface{}, error) {
	var fd, td time.Time
	q := r.URL.Query()
	if f, t := q.Get("dtstart"), q.Get("dtend"); len(f) > 0 && len(t) > 0 {
		var err error
		if fd, err = time.Parse(time.RFC3339, f); err != nil {
			return nil, err
		}
		if td, err = time.Parse(time.RFC3339, t); err != nil {
			return nil, err
		}
	} else {
		fd = time.Now().Truncate(time.Hour * 24)
		td = fd.Add(time.Hour * 24)
	}
	return hourglass.ListUplinks(db, fd, td, q["category[]"], q["status[]"])
}

func listTransfers(r *http.Request) (interface{}, error) {
	var fd, td time.Time
	q := r.URL.Query()
	if f, t := q.Get("dtstart"), q.Get("dtend"); len(f) > 0 && len(t) > 0 {
		var err error
		if fd, err = time.Parse(time.RFC3339, f); err != nil {
			return nil, err
		}
		if td, err = time.Parse(time.RFC3339, t); err != nil {
			return nil, err
		}
	} else {
		fd = time.Now().Truncate(time.Hour * 24)
		td = fd.Add(time.Hour * 24)
	}
	return hourglass.ListTransfers(db, fd, td, q["category[]"], q["status[]"])
}

func viewSlot(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	return hourglass.ViewSlot(db, id)
}

func viewDownlink(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	return hourglass.ViewDownlink(db, id)
}

func viewUplink(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	return hourglass.ViewUplink(db, id)
}

func viewTransfer(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	return hourglass.ViewTransfer(db, id)
}

func viewCategory(r *http.Request) (interface{}, error) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	return hourglass.ViewCategory(db, id)
}
