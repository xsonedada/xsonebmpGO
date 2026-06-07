package main

import (
	"net/http"
	"nexus-boost/database"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func AdminOrdersPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT o.id, u.username, o.title, o.game, o.total, o.status, o.created_at 
        FROM orders o 
        LEFT JOIN users u ON o.user_id = u.id 
        ORDER BY o.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")

	type OrderRow struct {
		ID        int
		Username  string
		Title     string
		Game      string
		Total     float64
		Status    string
		CreatedAt time.Time
	}

	var orders []OrderRow
	if rows != nil {
		for rows.Next() {
			var o OrderRow
			rows.Scan(&o.ID, &o.Username, &o.Title, &o.Game, &o.Total, &o.Status, &o.CreatedAt)
			orders = append(orders, o)
		}
	}
	if orders == nil {
		orders = []OrderRow{}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-orders",
		"User":   user,
		"Data":   gin.H{"Orders": orders},
	})
}

func AdminUpdateOrderStatus(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("UPDATE orders SET status=$1 WHERE id=$2", c.PostForm("status"), c.Param("id"))
	c.Redirect(302, "/admin/orders")
}

func AdminDeleteOrder(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM orders WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/orders")
}

func AdminBoostsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.price, b.rating, b.reviews, COALESCE(u.username, 'Нет') 
        FROM boosts b 
        LEFT JOIN users u ON b.user_id = u.id 
        ORDER BY b.id DESC
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type BoostRow struct {
		ID      int
		Game    string
		Title   string
		Price   float64
		Rating  float64
		Reviews int
		Owner   string
	}

	var boosts []BoostRow
	if rows != nil {
		for rows.Next() {
			var b BoostRow
			rows.Scan(&b.ID, &b.Game, &b.Title, &b.Price, &b.Rating, &b.Reviews, &b.Owner)
			boosts = append(boosts, b)
		}
	}
	if boosts == nil {
		boosts = []BoostRow{}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-boosts",
		"User":   user,
		"Data":   gin.H{"Boosts": boosts},
	})
}

func AdminDeleteBoost(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM boosts WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/boosts")
}

func adminReviewsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT r.id, r.rating, r.text, r.username, r.created_at, o.title 
        FROM reviews r 
        JOIN orders o ON r.order_id = o.id 
        ORDER BY r.created_at DESC
    `)
	defer rows.Close()

	user, _ := c.Get("user")

	type ReviewRow struct {
		ID        int
		Rating    int
		Text      string
		Username  string
		CreatedAt time.Time
		Title     string
	}

	var reviews []ReviewRow
	for rows.Next() {
		var r ReviewRow
		rows.Scan(&r.ID, &r.Rating, &r.Text, &r.Username, &r.CreatedAt, &r.Title)
		reviews = append(reviews, r)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-reviews",
		"User":   user,
		"Data":   gin.H{"Reviews": reviews},
	})
}

func adminDeleteReview(c *gin.Context) {
	database.DB.Exec("DELETE FROM reviews WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/reviews")
}

// Уведомления всем пользователям

func adminOrderDetail(c *gin.Context) {
	orderID := c.Param("id")
	user, _ := c.Get("user")

	var o Order
	var buyerName string
	err := database.DB.QueryRow(`
        SELECT o.id, o.user_id, o.boost_id, o.booster_id, o.title, o.game, 
               o.total, o.status, o.created_at, u.username
        FROM orders o 
        LEFT JOIN users u ON o.user_id = u.id 
        WHERE o.id = $1
    `, orderID).Scan(&o.ID, &o.UserID, &o.BoostID, &o.BoosterID, &o.Title, &o.Game,
		&o.Total, &o.Status, &o.CreatedAt, &buyerName)

	if err != nil {
		c.String(404, "Заказ не найден")
		return
	}

	// Получаем имя продавца
	var boosterName string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", o.BoosterID).Scan(&boosterName)
	o.Booster = boosterName

	// Сообщения
	rows, _ := database.DB.Query(
		"SELECT id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
	if rows != nil {
		defer rows.Close()
	}
	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "admin-order-detail",
		"Data": gin.H{
			"Order": gin.H{
				"ID":        o.ID,
				"Title":     o.Title,
				"Game":      o.Game,
				"Total":     o.Total,
				"Status":    o.Status,
				"BuyerName": buyerName,
				"Booster":   o.Booster,
				"CreatedAt": o.CreatedAt,
			},
			"Messages": messages,
		},
	})
}

// Просмотр сообщений заказа

func adminAddBoostPage(c *gin.Context) {

	user, _ := c.Get("user")

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "admin-add-boost",
	})
}

func adminAddBoost(c *gin.Context) {
	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	userID, _ := strconv.Atoi(c.PostForm("user_id"))

	database.DB.Exec("INSERT INTO boosts (game, title, description, price, booster, user_id) VALUES ($1,$2,$3,$4,'Admin',$5)",
		game, title, desc, price, userID)

	c.Redirect(302, "/admin/boosts")
}

// Экспорт пользователей

func adminRefundOrder(c *gin.Context) {
	orderID := c.Param("id")

	var userID int
	var total float64
	database.DB.QueryRow("SELECT user_id, total FROM orders WHERE id = $1", orderID).Scan(&userID, &total)

	// Возвращаем деньги
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", total, userID)
	database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

	// Запись транзакции
	database.DB.Exec("INSERT INTO transactions (user_id, type, amount, description) VALUES ($1, 'refund', $2, $3)",
		userID, total, "Возврат по заказу #"+orderID)

	// Лог админа
	session, _ := store.Get(c.Request, "xsonebmp-session")
	adminID, _ := session.Values["admin_id"]
	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'refund', $2)",
		adminID, "Возврат по заказу #"+orderID)

	c.Redirect(302, "/admin/orders?success=Возврат+оформлен")
}

// Редактирование товара админом

func adminEditBoostPage(c *gin.Context) {

	user, _ := c.Get("user")

	var b Boost
	database.DB.QueryRow("SELECT id, game, title, description, price FROM boosts WHERE id = $1", c.Param("id")).
		Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price)

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-edit-boost",
		"User":   user,
		"Data":   gin.H{"Boost": b},
	})
}

func adminEditBoost(c *gin.Context) {
	id := c.Param("id")
	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)

	database.DB.Exec("UPDATE boosts SET game=$1, title=$2, description=$3, price=$4 WHERE id=$5",
		game, title, desc, price, id)

	c.Redirect(302, "/admin/boosts")
}

// Создание пользователя админом

func adminVerifySeller(c *gin.Context) {
	userID := c.Param("id")
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS verified_sellers (
        user_id INTEGER PRIMARY KEY,
        verified_at TIMESTAMP DEFAULT NOW()
    )`)
	database.DB.Exec("INSERT INTO verified_sellers (user_id) VALUES ($1) ON CONFLICT DO NOTHING", userID)
	c.Redirect(302, "/admin/users?success=Продавец+верифицирован")
}

// Избранный товар

func adminFeatureBoost(c *gin.Context) {
	boostID := c.Param("id")
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS featured_boosts (
        boost_id INTEGER PRIMARY KEY,
        featured_at TIMESTAMP DEFAULT NOW()
    )`)
	database.DB.Exec("INSERT INTO featured_boosts (boost_id) VALUES ($1) ON CONFLICT DO NOTHING", boostID)
	c.Redirect(302, "/admin/boosts?success=Товар+добавлен+в+избранное")
}

// Получить уровень продавца

func adminVerifySellerAPI(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	giveBadge(userID, "verified", "Верифицированный продавец", "✅", "#1cd698")
	c.JSON(200, gin.H{"success": true})
}

// Авто-выдача бейджей при достижениях
