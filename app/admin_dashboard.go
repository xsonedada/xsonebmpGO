package main

import (
	"crypto/hmac"
	"log"
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func AdminLoginPage(c *gin.Context) {
	layoutHTML(c, http.StatusOK, gin.H{"Title": "XSoneBMP Админ", "Active": "admin-login"})
}

func AdminLogin(c *gin.Context) {
	if !rateLimitIP(c, adminLoginAttempts, &adminLoginMu, 5, 15*time.Minute) {
		layoutHTML(c, http.StatusOK, gin.H{
			"Title":  "XSoneBMP Админ",
			"Active": "admin-login",
			"Error":  "Слишком много попыток. Подождите 15 минут.",
		})
		return
	}

	username := c.PostForm("username")
	password := c.PostForm("password")

	usernameOK := hmac.Equal([]byte(username), []byte(adminUsername))
	passOK := bcrypt.CompareHashAndPassword([]byte(adminPassHash), []byte(password)) == nil

	if usernameOK && passOK {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		session.Values["admin_id"] = 1
		session.Values["admin_csrf"] = randomString(32)
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/admin")
		return
	}

	time.Sleep(300 * time.Millisecond)
	layoutHTML(c, http.StatusOK, gin.H{
		"Title":  "XSoneBMP Админ",
		"Active": "admin-login",
		"Error":  "Неверный логин или пароль",
	})
}

// adminRequired — middleware, проверяет наличие admin сессии

func AdminDashboard(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount, boostsCount, ordersCount int
	var totalEarnings float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&boostsCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE status = 'completed'").Scan(&totalEarnings)

	// Данные для графика
	rows, _ := database.DB.Query(`
        SELECT TO_CHAR(created_at, 'YYYY-MM') as month, COALESCE(SUM(total), 0)
        FROM orders WHERE status = 'completed' 
        AND created_at >= NOW() - INTERVAL '12 months'
        GROUP BY month ORDER BY month
    `)
	defer rows.Close()

	type ChartItem struct {
		Date    string
		Total   float64
		Percent int
	}
	var chartData []ChartItem
	var maxVal float64
	// Сначала собираем все данные
	for rows.Next() {
		var item ChartItem
		rows.Scan(&item.Date, &item.Total)
		chartData = append(chartData, item)
		if item.Total > maxVal {
			maxVal = item.Total
		}
	}
	// Затем вычисляем проценты
	for i := range chartData {
		if maxVal > 0 {
			chartData[i].Percent = int(chartData[i].Total * 100 / maxVal)
		}
	}

	// Последние заказы
	orderRows, _ := database.DB.Query(`
        SELECT o.id, o.title, o.total, o.status, u.username
        FROM orders o LEFT JOIN users u ON o.user_id = u.id
        ORDER BY o.created_at DESC LIMIT 10
    `)
	defer orderRows.Close()

	type RecentOrder struct {
		ID       int
		Title    string
		Total    float64
		Status   string
		Username string
	}
	var recentOrders []RecentOrder
	for orderRows.Next() {
		var o RecentOrder
		orderRows.Scan(&o.ID, &o.Title, &o.Total, &o.Status, &o.Username)
		recentOrders = append(recentOrders, o)
	}

	// Последние пользователи
	userRows, _ := database.DB.Query("SELECT id, username, email, balance, orders FROM users ORDER BY id DESC LIMIT 10")
	defer userRows.Close()

	type RecentUser struct {
		ID       int
		Username string
		Email    string
		Balance  float64
		Orders   int
	}
	var recentUsers []RecentUser
	for userRows.Next() {
		var u RecentUser
		userRows.Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Orders)
		recentUsers = append(recentUsers, u)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "admin",
		"Data": gin.H{
			"UsersCount":    usersCount,
			"BoostsCount":   boostsCount,
			"OrdersCount":   ordersCount,
			"TotalEarnings": totalEarnings,
			"ChartData":     chartData,
			"RecentOrders":  recentOrders,
			"RecentUsers":   recentUsers,
		},
	})
}

func AdminLogout(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["admin_id"] = nil
	session.Save(c.Request, c.Writer)
	c.Redirect(302, "/admin/login")
}

func adminNotifyPage(c *gin.Context) {
	user, _ := c.Get("user")

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "admin-notify",
	})
}

func adminSendNotify(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]
	message := c.PostForm("message")
	if message != "" {
		database.DB.Exec(
			"INSERT INTO notifications (user_id, text, link) SELECT id, $1, '' FROM users",
			message,
		)
		go func() {
			rows, _ := database.DB.Query("SELECT DISTINCT user_id FROM push_subscriptions")
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var uid int
					rows.Scan(&uid)
					sendPushNotification(uid, "XSoneBMP", message, "/")
				}
			}
		}()
	}

	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'mass_notify', $2)", userID, message)

	c.Redirect(302, "/admin?success=Уведомления+отправлены")
}

// Управление балансом

func adminSearch(c *gin.Context) {
	user, _ := c.Get("user")
	query := c.Query("q")
	if query == "" {
		// Передаём пустые результаты и сам запрос
		layoutHTML(c, http.StatusOK, gin.H{
			"Active": "admin-search",
			"User":   user,
			"Data": gin.H{
				"Users":  []gin.H{},
				"Orders": []gin.H{},
				"Boosts": []gin.H{},
				"Query":  query,
			},
		})
		return
	}

	searchPattern := "%" + query + "%"

	// Безопасный параметризованный запрос
	uRows, err := database.DB.Query(
		"SELECT id, username, email FROM users WHERE username ILIKE $1 OR email ILIKE $1 LIMIT 10",
		searchPattern,
	)
	if err != nil {
		log.Printf("❌ Search users error: %v", err)
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"Error": "Search failed"})
		return
	}
	defer uRows.Close()

	var users []gin.H
	var orders []gin.H
	var boosts []gin.H

	if query != "" {
		searchPattern := "%" + query + "%"

		uRows, _ := database.DB.Query(
			"SELECT id, username, email FROM users WHERE username ILIKE $1 OR email ILIKE $1 LIMIT 10",
			searchPattern,
		)
		if uRows != nil {
			defer uRows.Close()
			for uRows.Next() {
				var id int
				var name, email string
				uRows.Scan(&id, &name, &email)
				users = append(users, gin.H{"id": id, "name": name, "email": email})
			}
		}

		oRows, _ := database.DB.Query(
			"SELECT o.id, u.username, o.title, o.total FROM orders o JOIN users u ON o.user_id = u.id WHERE o.title ILIKE $1 LIMIT 10",
			searchPattern,
		)
		if oRows != nil {
			defer oRows.Close()
			for oRows.Next() {
				var id int
				var username, title string
				var total float64
				oRows.Scan(&id, &username, &title, &total)
				orders = append(orders, gin.H{"id": id, "username": username, "title": title, "total": total})
			}
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-search",
		"User":   user,
		"Data":   gin.H{"Users": users, "Orders": orders, "Boosts": boosts, "Query": query},
	})
}

// Детали заказа

func adminStatsPage(c *gin.Context) {
	var totalUsers, totalOrders, totalBoosts, completedOrders int
	var totalRevenue, avgOrderValue float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&totalUsers)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&totalOrders)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&totalBoosts)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE status = 'completed'").Scan(&completedOrders)
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE status = 'completed'").Scan(&totalRevenue)
	database.DB.QueryRow("SELECT COALESCE(AVG(total), 0) FROM orders").Scan(&avgOrderValue)

	user, _ := c.Get("user")

	// Заказов сегодня
	var todayOrders int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE DATE(created_at) = CURRENT_DATE").Scan(&todayOrders)

	// Новых пользователей сегодня
	var todayUsers int
	database.DB.QueryRow("SELECT COUNT(*) FROM users WHERE DATE(created_at) = CURRENT_DATE").Scan(&todayUsers)

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-stats",
		"User":   user,
		"Data": gin.H{
			"TotalUsers":      totalUsers,
			"TotalOrders":     totalOrders,
			"TotalBoosts":     totalBoosts,
			"CompletedOrders": completedOrders,
			"TotalRevenue":    totalRevenue,
			"AvgOrderValue":   avgOrderValue,
			"TodayOrders":     todayOrders,
			"TodayUsers":      todayUsers,
		},
	})
}

// Страница с графиками

func adminChartsPage(c *gin.Context) {
	// Данные по дням за последние 7 дней
	rows, _ := database.DB.Query(`
        SELECT DATE(created_at) as day, COUNT(*), COALESCE(SUM(total), 0)
        FROM orders 
        WHERE created_at >= NOW() - INTERVAL '7 days'
        GROUP BY DATE(created_at)
        ORDER BY day
    `)
	defer rows.Close()

	user, _ := c.Get("user")

	type DayData struct {
		Date  string
		Count int
		Total float64
	}

	var chartData []DayData
	for rows.Next() {
		var d DayData
		var day time.Time
		rows.Scan(&day, &d.Count, &d.Total)
		d.Date = day.Format("02.01")
		chartData = append(chartData, d)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-charts",
		"User":   user,
		"Data":   gin.H{"ChartData": chartData},
	})
}

// Транзакции

func adminTransactionsPage(c *gin.Context) {
	// Создаем таблицу если нужно
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS transactions (
        id SERIAL PRIMARY KEY,
        user_id INTEGER,
        type VARCHAR(50),
        amount DECIMAL(10,2),
        description TEXT,
        created_at TIMESTAMP DEFAULT NOW()
    )`)

	rows, _ := database.DB.Query(`
        SELECT t.id, u.username, t.type, t.amount, t.description, t.created_at 
        FROM transactions t 
        LEFT JOIN users u ON t.user_id = u.id 
        ORDER BY t.created_at DESC LIMIT 50
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type Transaction struct {
		ID          int
		Username    string
		Type        string
		Amount      float64
		Description string
		CreatedAt   time.Time
	}

	var transactions []Transaction
	if rows != nil {
		for rows.Next() {
			var t Transaction
			rows.Scan(&t.ID, &t.Username, &t.Type, &t.Amount, &t.Description, &t.CreatedAt)
			transactions = append(transactions, t)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-transactions",
		"User":   user,
		"Data":   gin.H{"Transactions": transactions},
	})
}

// Возврат средств

func adminViewLogs(c *gin.Context) {
	// Убедитесь что таблица создана
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS admin_logs (
        id SERIAL PRIMARY KEY,
        admin_id INTEGER,
        action TEXT,
        details TEXT,
        created_at TIMESTAMP DEFAULT NOW()
    )`)

	rows, _ := database.DB.Query(`
        SELECT a.id, a.admin_id, u.username, a.action, a.details, a.created_at 
        FROM admin_logs a 
        LEFT JOIN users u ON a.admin_id = u.id 
        ORDER BY a.created_at DESC LIMIT 100
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type LogEntry struct {
		ID        int
		AdminID   int
		Username  string
		Action    string
		Details   string
		CreatedAt time.Time
	}

	var logs []LogEntry
	if rows != nil {
		for rows.Next() {
			var l LogEntry
			rows.Scan(&l.ID, &l.AdminID, &l.Username, &l.Action, &l.Details, &l.CreatedAt)
			logs = append(logs, l)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-logs",
		"User":   user,
		"Data":   gin.H{"Logs": logs},
	})
}

// Настройки
