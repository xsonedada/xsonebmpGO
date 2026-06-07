package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func homePage(c *gin.Context) {
	user, _ := c.Get("user")

	// Используем ТОЛЬКО кеш
	stats := getCachedStats()
	usersCount := stats["users"].(int)
	ordersCount := stats["orders"].(int)
	avgRating := stats["rating"].(float64)

	// Все товары для PRO
	allRows, _ := database.DB.Query(`
    SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username,
           COALESCE((SELECT COUNT(*) FROM orders WHERE boost_id = b.id AND status = 'completed'), 0) as sales
    FROM boosts b 
    LEFT JOIN users u ON b.user_id = u.id 
    ORDER BY sales DESC, b.rating DESC
    LIMIT 48
`)
	if allRows != nil {
		defer allRows.Close()
	}

	var allBoosts []Boost
	if allRows != nil {
		for allRows.Next() {
			var b Boost
			var sales int
			allRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews, &b.BoosterID, &b.OwnerName, &sales)
			allBoosts = append(allBoosts, b)
		}
	}

	// PRO товары
	proRows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username
        FROM boosts b 
        JOIN users u ON b.user_id = u.id 
        JOIN seller_profiles sp ON u.id = sp.user_id 
        WHERE sp.is_pro = true
        ORDER BY b.rating DESC LIMIT 4
    `)
	if proRows != nil {
		defer proRows.Close()
	}

	var proBoosts []Boost
	if proRows != nil {
		for proRows.Next() {
			var b Boost
			proRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews, &b.BoosterID, &b.OwnerName)
			proBoosts = append(proBoosts, b)
		}
	}

	// Последние отзывы
	revRows, _ := database.DB.Query(`
    SELECT r.rating, r.text, r.username, r.created_at, o.title as order_title
    FROM reviews r 
    JOIN orders o ON r.order_id = o.id 
    ORDER BY r.created_at DESC LIMIT 10
`)
	if revRows != nil {
		defer revRows.Close()
	}

	type ReviewItem struct {
		Rating     int
		Text       string
		Username   string
		CreatedAt  time.Time
		OrderTitle string
	}

	var latestReviews []ReviewItem
	if revRows != nil {
		for revRows.Next() {
			var r ReviewItem
			revRows.Scan(&r.Rating, &r.Text, &r.Username, &r.CreatedAt, &r.OrderTitle)
			latestReviews = append(latestReviews, r)
		}
	}

	// Топ-4 (исключая PRO)
	var top []Boost
	for _, b := range allBoosts {
		if len(top) < 4 {
			top = append(top, b)
		}
	}

	// Минимальные цены по играм из уже загруженных allBoosts
	gamePrices := make(map[string]float64)
	for _, b := range allBoosts {
		if current, ok := gamePrices[b.Game]; !ok || b.Price < current {
			gamePrices[b.Game] = b.Price
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "home",
		"Data": gin.H{
			"TopBoosts":     top,
			"ProBoosts":     proBoosts,
			"LatestReviews": latestReviews,
			"GamePrices":    gamePrices,
			"Stats": gin.H{
				"Бустеров": fmt.Sprintf("%d", usersCount),
				"Заказов":  fmt.Sprintf("%d", ordersCount),
				"Рейтинг":  fmt.Sprintf("%.1f/5", avgRating),
				"Онлайн":   fmt.Sprintf("%d", usersCount/2+1),
			},
		},
	})
}

func marketplacePage(c *gin.Context) {
	user, _ := c.Get("user")
	rows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username, u.reviews as seller_reviews
        FROM boosts b 
        LEFT JOIN users u ON b.user_id = u.id 
        ORDER BY b.id DESC
        LIMIT 100
    `)
	if rows != nil {
		defer rows.Close()
	}

	var boosts []gin.H
	if rows != nil {
		for rows.Next() {
			var id, userID, reviews, sellerReviews int
			var game, title, desc, ownerName string
			var price, rating float64
			rows.Scan(&id, &game, &title, &desc, &price, &rating, &reviews, &userID, &ownerName, &sellerReviews)
			boosts = append(boosts, gin.H{
				"ID": id, "Game": game, "Title": title, "Description": desc,
				"Price": price, "Rating": rating, "Reviews": reviews,
				"BoosterID": userID, "OwnerName": ownerName,
				"TotalSellerReviews": sellerReviews,
			})
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "marketplace",
		"Boosts": boosts,
	})
}

func aboutPage(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount, ordersCount, boostsCount, gamesCount int
	var avgRating float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&boostsCount)
	database.DB.QueryRow("SELECT COUNT(DISTINCT game) FROM boosts WHERE game IS NOT NULL AND game != ''").Scan(&gamesCount)
	database.DB.QueryRow("SELECT COALESCE(AVG(rating), 0) FROM users WHERE rating > 0").Scan(&avgRating)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "about",
		"Data": gin.H{
			"UsersCount":  usersCount,
			"OrdersCount": ordersCount,
			"BoostsCount": boostsCount,
			"GamesCount":  gamesCount,
			"Рейтинг":     avgRating,
		},
	})
}

func contactPage(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount int

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "contact",
		"Data": gin.H{
			"UsersCount": usersCount,
		},
	})
}

// Аутентификация

func topUpBalance(c *gin.Context) {
	c.Redirect(302, "/profile?error=Пополнение+через+платёжную+систему+пока+недоступно")
}

func withdrawBalance(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}
	if !validateUserCSRF(c) {
		c.Redirect(302, "/profile?error=Ошибка+безопасности")
		return
	}
	amount, err := strconv.ParseFloat(c.PostForm("amount"), 64)
	if err != nil || amount <= 0 {
		c.Redirect(302, "/profile?error=Некорректная+сумма")
		return
	}
	var bal float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id=$1", userID).Scan(&bal)
	if amount > bal {
		c.Redirect(302, "/profile?error=Недостаточно+средств")
		return
	}
	database.DB.Exec("UPDATE users SET balance=balance-$1 WHERE id=$2", amount, userID)
	c.Redirect(302, "/profile?success=Заявка+на+вывод+принята")
}
