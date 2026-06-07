package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func render(c *gin.Context, tmpl string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	if _, ok := data["User"]; !ok {
		user, _ := c.Get("user")
		data["User"] = user
	}
	data["Active"] = tmpl
	data["csrf"] = ensureUserCSRF(c)
	c.HTML(http.StatusOK, "layout.html", data)
}

// Обработчики страниц

func isAdmin(c *gin.Context) bool {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		return false
	}

	// Админы: пользователь с ID=1 (или добавьте другие ID)
	return userID == 1
}

func adminRequired(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	if _, ok := session.Values["admin_id"]; !ok {
		c.Redirect(http.StatusFound, "/admin/login")
		c.Abort()
		return
	}
	c.Next()
}

func layoutHTML(c *gin.Context, status int, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	active, _ := data["Active"].(string)
	if strings.HasPrefix(c.Request.URL.Path, "/admin") || strings.HasPrefix(active, "admin") {
		data["csrf"] = ensureAdminCSRF(c)
	} else {
		data["csrf"] = ensureUserCSRF(c)
	}
	c.HTML(status, "layout.html", data)
}

func isAdminSession(c *gin.Context) bool {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	_, ok := session.Values["admin_id"]
	return ok
}
