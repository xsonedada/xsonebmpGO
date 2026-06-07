package main

import (
	"net/http"
	"nexus-boost/database"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func adminPromocodesPage(c *gin.Context) {
	rows, _ := database.DB.Query("SELECT id, code, discount_percent, max_uses, used_count, is_active, created_at FROM promocodes ORDER BY id DESC")
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")
	type Promo struct {
		ID              int
		Code            string
		DiscountPercent int
		MaxUses         int
		UsedCount       int
		IsActive        bool
		CreatedAt       time.Time
	}

	var promos []Promo
	if rows != nil {
		for rows.Next() {
			var p Promo
			rows.Scan(&p.ID, &p.Code, &p.DiscountPercent, &p.MaxUses, &p.UsedCount, &p.IsActive, &p.CreatedAt)
			promos = append(promos, p)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-promocodes",
		"User":   user,
		"Data":   gin.H{"Promos": promos},
	})
}

func adminCreatePromocode(c *gin.Context) {
	code := c.PostForm("code")
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	maxUses, _ := strconv.Atoi(c.PostForm("max_uses"))

	database.DB.Exec("INSERT INTO promocodes (code, discount_percent, max_uses) VALUES ($1,$2,$3)",
		code, discount, maxUses)

	c.Redirect(302, "/admin/promocodes")
}

func adminDeletePromocode(c *gin.Context) {
	database.DB.Exec("DELETE FROM promocodes WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/promocodes")
}

func adminTogglePromocode(c *gin.Context) {
	database.DB.Exec("UPDATE promocodes SET is_active = NOT is_active WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/promocodes")
}

func adminSalesPage(c *gin.Context) {
	// Активные акции
	salesRows, _ := database.DB.Query("SELECT id, name, discount_percent, start_date, end_date, is_active FROM sales ORDER BY id DESC")
	if salesRows != nil {
		defer salesRows.Close()
	}

	user, _ := c.Get("user")

	type Sale struct {
		ID        int
		Name      string
		Discount  int
		StartDate time.Time
		EndDate   time.Time
		IsActive  bool
	}

	var sales []Sale
	if salesRows != nil {
		for salesRows.Next() {
			var s Sale
			salesRows.Scan(&s.ID, &s.Name, &s.Discount, &s.StartDate, &s.EndDate, &s.IsActive)
			sales = append(sales, s)
		}
	}

	// Скидка на первый заказ
	var firstDiscount int
	var firstActive bool
	database.DB.QueryRow("SELECT discount_percent, is_active FROM first_order_discount LIMIT 1").Scan(&firstDiscount, &firstActive)

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-sales",
		"User":   user,
		"Data": gin.H{
			"Sales":         sales,
			"FirstDiscount": firstDiscount,
			"FirstActive":   firstActive,
		},
	})
}

func adminCreateSale(c *gin.Context) {
	name := c.PostForm("name")
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	startDate := c.PostForm("start_date")
	endDate := c.PostForm("end_date")

	database.DB.Exec("INSERT INTO sales (name, discount_percent, start_date, end_date) VALUES ($1,$2,$3,$4)",
		name, discount, startDate, endDate)

	c.Redirect(302, "/admin/sales")
}

func adminToggleSale(c *gin.Context) {
	database.DB.Exec("UPDATE sales SET is_active = NOT is_active WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/sales")
}

func adminDeleteSale(c *gin.Context) {
	database.DB.Exec("DELETE FROM sales WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/sales")
}
