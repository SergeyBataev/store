package models

import (
	"encoding/json"
	"errors"
	"github.com/goincremental/negroni-sessions"
	"gopkg.in/gorp.v2"
	"net/http"
	"time"
)

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

func NewPage() Page {
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

func (b *Basket) CalcTotal() {
	b.Total = 0
	for _, value := range b.Items {
		b.Total += value.Quantity * value.Price
	}
}

func (p *Page) CheckOut(username string, dbmap *gorp.DbMap) error {
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

func GetProducts(p *[]Product, r *http.Request, dbmap *gorp.DbMap) (*[]Product, error) {
	orderBy := " order by "
	orderBy += (GetStringFromSession(r, "OrderBy"))
	if _, err := dbmap.Select(p, "select * from products"+orderBy); err != nil {
		return &[]Product{}, err
	}

	return p, nil
}

func GetStringFromSession(r *http.Request, key string) string {
	valueString := ""
	if val := sessions.GetSession(r).Get(key); val != nil {
		valueString = val.(string)
	}

	return valueString
}
