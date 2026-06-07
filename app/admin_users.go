package main

import (
	"bufio"
	"fmt"
	"net/http"
	"nexus-boost/database"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func AdminUsersPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
    SELECT u.id, u.username, u.email, u.balance, u.orders, u.rating, 
           CASE WHEN b.user_id IS NOT NULL THEN true ELSE false END as banned
    FROM users u 
    LEFT JOIN banned_users b ON u.id = b.user_id 
    ORDER BY u.id DESC
`)
	defer rows.Close()

	user, _ := c.Get("user")

	type UserRow struct {
		ID       int
		Username string
		Email    string
		Balance  float64
		Orders   int
		Rating   float64
		Banned   bool
	}

	var users []UserRow
	for rows.Next() {
		var u UserRow
		rows.Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Orders, &u.Rating, &u.Banned)
		users = append(users, u)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-users",
		"User":   user,
		"Data":   gin.H{"Users": users},
	})
}

func AdminEditUser(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Redirect(302, "/admin/users?error=Неверный+ID")
		return
	}

	username := c.PostForm("username")
	email := c.PostForm("email")
	balance, _ := strconv.ParseFloat(c.PostForm("balance"), 64)

	database.DB.Exec(
		"UPDATE users SET username=$1, email=$2, balance=$3 WHERE id=$4",
		username, email, balance, userID,
	)

	c.Redirect(302, "/admin/users?success=Пользователь+обновлён")
}

func AdminDeleteUser(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM users WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/users")
}

func adminBanUser(c *gin.Context) {
	userID := c.Param("id")
	database.DB.Exec("INSERT INTO banned_users (user_id, reason) VALUES ($1, 'Нарушение правил') ON CONFLICT DO NOTHING", userID)
	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'ban', 'Забанил пользователя #'+$2)", 1, userID)
	c.Redirect(302, "/admin/users")
}

func adminUnbanUser(c *gin.Context) {
	database.DB.Exec("DELETE FROM banned_users WHERE user_id = $1", c.Param("id"))
	c.Redirect(302, "/admin/users")
}

func adminMakePro(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Redirect(302, "/admin/users")
		return
	}
	database.DB.Exec("INSERT INTO seller_profiles (user_id, is_pro) VALUES ($1, true) ON CONFLICT (user_id) DO UPDATE SET is_pro = true", userID)
	giveBadge(userID, "pro", "PRO продавец", "👑", "#fbbf24")
	c.Redirect(302, "/admin/users")
}

// Поиск по всем таблицам

func adminExportUsers(c *gin.Context) {
	rows, _ := database.DB.Query("SELECT id, username, email, balance, orders, created_at FROM users ORDER BY id")
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=users_export.csv")

	writer := bufio.NewWriter(c.Writer)
	defer writer.Flush()

	writer.WriteString("ID,Имя,Email,Баланс,Заказов,Дата регистрации\n")

	for rows.Next() {
		var id, orders int
		var name, email string
		var balance float64
		var createdAt time.Time
		rows.Scan(&id, &name, &email, &balance, &orders, &createdAt)
		fmt.Fprintf(writer, "%d,%s,%s,%.2f,%d,%s\n", id, name, email, balance, orders, createdAt.Format("02.01.2006"))
	}
}

// Расширенная статистика

func adminCreateUser(c *gin.Context) {
	username := c.PostForm("username")
	email := c.PostForm("email")
	password := c.PostForm("password")
	balance, _ := strconv.ParseFloat(c.PostForm("balance"), 64)

	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	database.DB.Exec("INSERT INTO users (username, email, password, balance) VALUES ($1,$2,$3,$4)",
		username, email, string(hash), balance)

	c.Redirect(302, "/admin/users?success=Пользователь+создан")
}

// Логи админа

func adminAssignRole(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	role := c.PostForm("role")

	database.DB.Exec("INSERT INTO user_roles (user_id, role, assigned_by) VALUES ($1,$2,1)", userID, role)

	c.Redirect(302, "/admin/users")
}

// Снять роль

func adminRemoveRole(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	role := c.Query("role")

	database.DB.Exec("DELETE FROM user_roles WHERE user_id = $1 AND role = $2", userID, role)

	c.Redirect(302, "/admin/users")
}

// Страница редактирования профиля продавца

func adminEditUserPage(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.String(400, "Неверный ID пользователя")
		return
	}

	var u User
	err = database.DB.QueryRow(
		"SELECT id, username, email, balance, level, orders FROM users WHERE id = $1",
		userID,
	).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders)

	if err != nil {
		c.String(404, "Пользователь не найден")
		return
	}

	var isBanned bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM banned_users WHERE user_id = $1)", userID).Scan(&isBanned)

	currentUser, _ := c.Get("user")

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   currentUser,
		"Active": "admin-edit-user",
		"Data": gin.H{
			"EditUser": u,
			"IsBanned": isBanned,
		},
	})
}
