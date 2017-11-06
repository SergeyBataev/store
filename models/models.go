// package contains shop models structs and their management functions
package models

import (
	"encoding/json"
	"errors"
	"github.com/goincremental/negroni-sessions"
	"gopkg.in/gorp.v2"
	"net/http"
	"time"
)

// represents all info for web page
type Page struct {
	Products   []Product
	UserBasket Basket
	User       Userinfo
}

/*
	method provides user's basket and wallet validation,
	basket emptying in case of positive validation
	and updating database and returning nil
	or returning error in case of bad validation or if database updation fails
*/
func (p *Page) CheckOut(dbmap *gorp.DbMap) error {
	username := p.User.Name
	if p.UserBasket.Total > p.User.Wallet {
		return errors.New("Not enough money in your wallet!")
	}

	productsInStock := make([]*Product, 0)
	for _, value := range p.UserBasket.Items {
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

	p.User.Wallet -= p.UserBasket.Total
	if _, err := dbmap.Db.Exec("update users set wallet=? where username=?", p.User.Wallet, p.User.Name); err != nil {
		return err
	}

	for _, value := range productsInStock {
		if _, err := dbmap.Update(value); err != nil {
			return err
		}
	}

	basketjson, err := json.Marshal(p.UserBasket)
	if err != nil {
		return err
	}

	order := Order{ID: -1, User: username, Date: time.Now().Format("2006-01-02"), BasketJSON: basketjson}
	if err = dbmap.Insert(&order); err != nil {
		return err
	}
	p.UserBasket.Items = make(map[int]*Product)
	return nil
}

// struct represents product in stock
type Product struct {
	PK       int64  `db:"pk" json:"pk"`
	Title    string `db:"title" json:"title"`
	Type     string `db:"type" json:"type"`
	Price    int64  `db:"price" json:"price"`
	Quantity int64  `db:"quantity" json:"quantity"`
}

// method return pointer to slice which contains all products in stock or error if fetching of products from database failes
func GetProducts(p *[]Product, r *http.Request, dbmap *gorp.DbMap) (*[]Product, error) {
	orderBy := " order by "
	orderBy += (GetStringFromSession(r, "OrderBy"))
	if _, err := dbmap.Select(p, "select * from products"+orderBy); err != nil {
		return &[]Product{}, err
	}

	return p, nil
}

// represents user's basket
type Basket struct {
	Items map[int]*Product `json:"items"`
	Total int64            `json:"total"`
}

func (b *Basket) CalcTotal() {
	b.Total = 0
	for _, value := range b.Items {
		b.Total += value.Quantity * value.Price
	}
}

type Userinfo struct {
	Name   string
	Wallet int64
}

type User struct {
	Username string `db:"username"`
	Secret   []byte `db:"secret"`
	Wallet   int64  `db:"wallet"`
}

// this struct is warn user about bad login validation
type LoginPage struct {
	Error string
}

// this struct is used for record orders history into database
type Order struct {
	ID         int64  `db:"id"`
	User       string `db:"user"`
	Date       string `db:"checked"`
	BasketJSON []byte `db:"basketjson"`
}

func GetStringFromSession(r *http.Request, key string) string {
	valueString := ""
	if val := sessions.GetSession(r).Get(key); val != nil {
		valueString = val.(string)
	}

	return valueString
}
