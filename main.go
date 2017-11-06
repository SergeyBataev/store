package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strconv"

	"encoding/json"
	"github.com/goincremental/negroni-sessions"
	"github.com/goincremental/negroni-sessions/cookiestore"
	gmux "github.com/gorilla/mux"
	"github.com/urfave/negroni"
	"html/template"
	"net/http"

	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"gopkg.in/gorp.v2"

	"models"
)

var dbmap *gorp.DbMap
var page models.Page
var templates = template.Must(template.ParseFiles("templates/index.html", "templates/login.html"))

func initDB() {
	db, _ := sql.Open("mysql", "root:root@tcp(localhost:3306)/shop?charset=utf8")
	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.MySQLDialect{Engine: "InnoDB", Encoding: "UTF8"}}

	dbmap.AddTableWithName(models.Product{}, "products").SetKeys(true, "pk")
	dbmap.AddTableWithName(models.User{}, "users").SetKeys(false, "username")
	dbmap.AddTableWithName(models.Order{}, "orders").SetKeys(true, "id")
	dbmap.CreateTablesIfNotExists()
}

func main() {
	fmt.Println("Server is started...")
	initDB()
	page = models.NewPage()
	defer dbmap.Db.Close()

	mux := gmux.NewRouter()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page.Products = make([]models.Product, 0)
		if _, err := models.GetProducts(&page.Products, r, dbmap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page.UserBasket.CalcTotal()
		if err := templates.ExecuteTemplate(w, "index.html", page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}).Methods("GET")

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var p models.LoginPage
		if r.FormValue("register") != "" {
			secret, _ := bcrypt.GenerateFromPassword([]byte(r.FormValue("secret")), bcrypt.DefaultCost)
			user := models.User{Username: r.FormValue("username"), Secret: secret, Wallet: 100}

			if err := dbmap.Insert(&user); err != nil {
				p.Error = err.Error()
			} else {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
		} else if r.FormValue("login") != "" {
			username := r.FormValue("username")
			user, err := dbmap.Get(models.User{}, username)
			if err != nil {
				p.Error = err.Error()
			} else if user == nil {
				p.Error = "No such user with Username: " + username
			} else {
				u := user.(*models.User)
				if err := bcrypt.CompareHashAndPassword(u.Secret, []byte(r.FormValue("secret"))); err != nil {
					p.Error = err.Error()
				} else {
					sessions.GetSession(r).Set("User", username)
					page.User = models.Userinfo{Name: username, Wallet: u.Wallet}
					http.Redirect(w, r, "/", http.StatusFound)
					return
				}
			}
		}

		if err := templates.ExecuteTemplate(w, "login.html", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		sessions.GetSession(r).Set("User", nil)
		page.User = models.Userinfo{}
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	mux.HandleFunc("/products", func(w http.ResponseWriter, r *http.Request) {
		columnName := r.FormValue("orderBy")
		if columnName != "title" && columnName != "type" && columnName != "quantity" && columnName != "price" {
			columnName = "pk"
		}
		sessions.GetSession(r).Set("OrderBy", columnName)

		products := make([]models.Product, 0)
		if _, err := models.GetProducts(&products, r, dbmap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := json.NewEncoder(w).Encode(&products); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("GET").Queries("orderBy", "{orderBy:title|type|quantity|price}")

	mux.HandleFunc("/basket/checkout", func(w http.ResponseWriter, r *http.Request) {
		if err := page.CheckOut(models.GetStringFromSession(r, "User"), dbmap); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/", http.StatusOK)
	}).Methods("POST")

	mux.HandleFunc("/basket/{pk}", func(w http.ResponseWriter, r *http.Request) {
		pk, err := strconv.Atoi((gmux.Vars(r)["pk"]))

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if _, ok := page.UserBasket.Items[pk]; !ok {
			prod, err := dbmap.Get(models.Product{}, pk)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			page.UserBasket.Items[pk] = prod.(*models.Product)
			page.UserBasket.Items[pk].Quantity = 1
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}).Methods("PUT")

	mux.HandleFunc("/basket", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		delete(page.UserBasket.Items, id)
		w.WriteHeader(http.StatusOK)
	}).Methods("DELETE")

	mux.HandleFunc("/basket", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		quantity, err := strconv.ParseInt(r.FormValue("quantity"), 10, 64)
		if err != nil || quantity < 1 {
			page.UserBasket.Items[id].Quantity = int64(1)
		} else {
			page.UserBasket.Items[id].Quantity = quantity
		}

		w.WriteHeader(http.StatusOK)
	}).Methods("POST")

	n := negroni.Classic()
	n.Use(sessions.Sessions("go-for-web-dev", cookiestore.New([]byte("my-secret-123"))))
	n.Use(negroni.HandlerFunc(verifyDatabase))
	n.Use(negroni.HandlerFunc(verifyUser))
	n.UseHandler(mux)
	n.Run(":8080")
}

func verifyDatabase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	err := dbmap.Db.Ping()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	next(w, r)
}

func verifyUser(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if r.URL.Path == "/login" {
		next(w, r)
		return
	}

	if u := models.GetStringFromSession(r, "User"); u != "" {
		if u == page.User.Name {
			if _, err := dbmap.Get(models.User{}, u); err == nil {
				next(w, r)
				return
			}
		}
	}

	http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
}
