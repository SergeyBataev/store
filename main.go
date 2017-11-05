package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strconv"

	// "encoding/json"
	"html/template"
	"net/http"

	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"gopkg.in/gorp.v2"

	gmux "github.com/gorilla/mux"
	"github.com/urfave/negroni"
)

var dbmap *gorp.DbMap
var page Page
var templates = template.Must(template.ParseFiles("templates/index.html", "templates/login.html"))

type Product struct {
	PK          int64  `db:"pk"`
	Title       string `db:"title"`
	Description string `db:"description"`
	Price       int64  `db:"price"`
	Quantity    int64  `db:"quantity"`
}

type Page struct {
	Products   []Product
	UserBasket Basket
}

type Basket struct {
	Items map[int]*Product
	Total int64
}

func (b *Basket) calcTotal() {
	b.Total = 0
	for _, value := range b.Items {
		b.Total += value.Quantity * value.Price
	}
}

type User struct {
	Username string `db:"username"`
	Secret   []byte `db:"secret"`
}

type LoginPage struct {
	Error string
}

func initDB() {
	db, _ := sql.Open("mysql", "root:root@tcp(localhost:3306)/shop?charset=utf8")
	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.MySQLDialect{Engine: "InnoDB", Encoding: "UTF8"}}

	dbmap.AddTableWithName(Product{}, "products").SetKeys(true, "pk")
	dbmap.AddTableWithName(User{}, "users").SetKeys(false, "username")
	dbmap.CreateTablesIfNotExists()
}

func newPage() Page {
	return Page{Products: make([]Product, 0), UserBasket: Basket{Items: make(map[int]*Product)}}
}

func main() {
	fmt.Println("Server is started...")
	initDB()
	page = newPage()
	defer dbmap.Db.Close()

	mux := gmux.NewRouter()

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var p LoginPage
		if r.FormValue("register") != "" {
			secret, _ := bcrypt.GenerateFromPassword([]byte(r.FormValue("secret")), bcrypt.DefaultCost)
			user := User{Username: r.FormValue("username"), Secret: secret}

			if err := dbmap.Insert(&user); err != nil {
				p.Error = err.Error()
			} else {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		} else if r.FormValue("login") != "" {
			user, err := dbmap.Get(User{}, r.FormValue("username"))
			if err != nil {
				p.Error = err.Error()
			} else if user == nil {
				p.Error = "No such user with Username: " + r.FormValue("username")
			} else {
				u := user.(*User)
				if err := bcrypt.CompareHashAndPassword(u.Secret, []byte(r.FormValue("secret"))); err != nil {
					p.Error = err.Error()
				} else {
					http.Redirect(w, r, "/", http.StatusFound)
					return
				}
			}
		}

		if err := templates.ExecuteTemplate(w, "login.html", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page.Products = make([]Product, 0)
		if _, err := dbmap.Select(&page.Products, "select * from products"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page.UserBasket.calcTotal()
		if err := templates.ExecuteTemplate(w, "index.html", page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}).Methods("GET")

	mux.HandleFunc("/basket/checkout", func(w http.ResponseWriter, r *http.Request) {
		productsInStock := make([]Product, 0)
		for _, value := range page.UserBasket.Items {
			var p Product
			err := dbmap.SelectOne(&p, "select * from products where pk=?", value.PK)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			p.Quantity -= value.Quantity
			if p.Quantity < 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			productsInStock = append(productsInStock, p)
		}

		for _, value := range productsInStock {
			if _, err := dbmap.Update(&value); err != nil {
				fmt.Println(err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

		page.UserBasket.Items = make(map[int]*Product)
		w.WriteHeader(http.StatusOK)

	}).Methods("POST")

	mux.HandleFunc("/basket/{pk}", func(w http.ResponseWriter, r *http.Request) {
		pk, err := strconv.Atoi((gmux.Vars(r)["pk"]))

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if _, ok := page.UserBasket.Items[pk]; !ok {
			prod, err := dbmap.Get(Product{}, pk)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			page.UserBasket.Items[pk] = prod.(*Product)
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
	// n.Use(sessions.Sessions("go-for-web-dev", cookiestore.New([]byte("my-secret-123"))))
	n.Use(negroni.HandlerFunc(verifyDatabase))
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
