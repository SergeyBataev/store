package main

import (
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strconv"
	"time"

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
)

var dbmap *gorp.DbMap
var page Page
var templates = template.Must(template.ParseFiles("templates/index.html", "templates/login.html"))

type Product struct {
	PK       int64  `db:"pk" json:"pk"`
	Title    string `db:"title" json:"title"`
	Type     string `db:"type" json:"type"`
	Price    int64  `db:"price" json:"price"`
	Quantity int64  `db:"quantity" json:"quantity"`
}

type Page struct {
	Products   []Product
	UserBasket Basket
	User       Userinfo
}

func newPage() Page {
	return Page{UserBasket: Basket{Items: make(map[int]*Product)}}
}

type Userinfo struct {
	Name   string
	Wallet int64
}

type Basket struct {
	Items map[int]*Product `json:"items"`
	Total int64            `json:"total"`
}

func (b *Basket) calcTotal() {
	b.Total = 0
	for _, value := range b.Items {
		b.Total += value.Quantity * value.Price
	}
}

func (b *Basket) checkOut(username string) error {
	if b.Total > page.User.Wallet {
		return errors.New("Not enough money in your wallet!")
	}

	productsInStock := make([]*Product, 0)
	for _, value := range b.Items {
		prod, err := dbmap.Get(Product{}, value.PK)
		if err != nil {
			return err
		}

		product := prod.(*Product)

		if value.Quantity > product.Quantity {
			return errors.New("Not enough quantity in stock. In stock: " + string(product.Quantity))
		}
		product.Quantity -= value.Quantity
		productsInStock = append(productsInStock, product)
	}

	page.User.Wallet -= b.Total
	if _, err := dbmap.Db.Exec("update users set wallet=? where username=?", page.User.Wallet, page.User.Name); err != nil {
		return err
	}

	for _, value := range productsInStock {
		if _, err := dbmap.Update(value); err != nil {
			fmt.Println("err")
			return err
		}
	}

	basketjson, err := json.Marshal(b)
	if err != nil {
		return err
	}

	order := Order{ID: -1, User: username, Date: time.Now().Format("2006-01-02"), BasketJSON: basketjson}
	if err = dbmap.Insert(&order); err != nil {
		return err
	}
	b.Items = make(map[int]*Product)
	return nil
}

type User struct {
	Username string `db:"username"`
	Secret   []byte `db:"secret"`
	Wallet   int64  `db:"wallet"`
}

type LoginPage struct {
	Error string
}

type Order struct {
	ID         int64  `db:"id"`
	User       string `db:"user"`
	Date       string `db:"checked"`
	BasketJSON []byte `db:"basketjson"`
}

func initDB() {
	db, _ := sql.Open("mysql", "root:root@tcp(localhost:3306)/shop?charset=utf8")
	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.MySQLDialect{Engine: "InnoDB", Encoding: "UTF8"}}

	dbmap.AddTableWithName(Product{}, "products").SetKeys(true, "pk")
	dbmap.AddTableWithName(User{}, "users").SetKeys(false, "username")
	dbmap.AddTableWithName(Order{}, "orders").SetKeys(true, "id")
	dbmap.CreateTablesIfNotExists()
}

func GetProducts(p *[]Product, r *http.Request) (*[]Product, error) {
	orderBy := " order by "
	orderBy += (GetStringFromSession(r, "OrderBy"))
	fmt.Println(orderBy)
	if _, err := dbmap.Select(p, "select * from products"+orderBy); err != nil {
		return &[]Product{}, err
	}

	return p, nil
}

func main() {
	fmt.Println("Server is started...")
	initDB()
	page = newPage()
	defer dbmap.Db.Close()

	mux := gmux.NewRouter()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page.Products = make([]Product, 0)
		if _, err := GetProducts(&page.Products, r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page.UserBasket.calcTotal()
		if err := templates.ExecuteTemplate(w, "index.html", page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}).Methods("GET")

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var p LoginPage
		if r.FormValue("register") != "" {
			secret, _ := bcrypt.GenerateFromPassword([]byte(r.FormValue("secret")), bcrypt.DefaultCost)
			user := User{Username: r.FormValue("username"), Secret: secret, Wallet: 100}

			if err := dbmap.Insert(&user); err != nil {
				p.Error = err.Error()
			} else {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		} else if r.FormValue("login") != "" {
			username := r.FormValue("username")
			user, err := dbmap.Get(User{}, username)
			if err != nil {
				p.Error = err.Error()
			} else if user == nil {
				p.Error = "No such user with Username: " + username
			} else {
				u := user.(*User)
				if err := bcrypt.CompareHashAndPassword(u.Secret, []byte(r.FormValue("secret"))); err != nil {
					p.Error = err.Error()
				} else {
					sessions.GetSession(r).Set("User", username)
					page.User = Userinfo{Name: username, Wallet: u.Wallet}
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
		page.User = Userinfo{}
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	mux.HandleFunc("/products", func(w http.ResponseWriter, r *http.Request) {
		columnName := r.FormValue("orderBy")
		if columnName != "title" && columnName != "type" && columnName != "quantity" && columnName != "price" {
			columnName = "pk"
		}
		sessions.GetSession(r).Set("OrderBy", columnName)

		products := make([]Product, 0)
		if _, err := GetProducts(&products, r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := json.NewEncoder(w).Encode(&products); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("GET").Queries("orderBy", "{orderBy:title|type|quantity|price}")

	mux.HandleFunc("/basket/checkout", func(w http.ResponseWriter, r *http.Request) {
		if err := page.UserBasket.checkOut(GetStringFromSession(r, "User")); err != nil {
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

	if u := GetStringFromSession(r, "User"); u != "" {
		if u == page.User.Name {
			if _, err := dbmap.Get(User{}, u); err == nil {
				next(w, r)
				return
			}
		}
	}

	http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
}

func GetStringFromSession(r *http.Request, key string) string {
	valueString := ""
	if val := sessions.GetSession(r).Get(key); val != nil {
		valueString = val.(string)
	}

	return valueString
}
