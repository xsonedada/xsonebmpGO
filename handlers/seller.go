package handlers

import (
	"encoding/json"
	"net/http"
	"nexus-boost/models"
)

// GET /api/pro-settings - получить настройки PRO
func GetProSettings(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*models.User)
	if !user.IsPro {
		http.Error(w, "Требуется PRO подписка", http.StatusForbidden)
		return
	}

	json.NewEncoder(w).Encode(user.ProSettings)
}

// POST /api/pro-settings - сохранить настройки PRO
func SaveProSettings(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*models.User)
	if !user.IsPro {
		http.Error(w, "Требуется PRO подписка", http.StatusForbidden)
		return
	}

	var settings models.ProSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Сохраняем в БД
	err := db.UpdateUserProSettings(user.ID, settings)
	if err != nil {
		http.Error(w, "Ошибка сохранения", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// GET /seller/pro-settings - страница редактора
func ProSettingsPage(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*models.User)
	if !user.IsPro {
		http.Redirect(w, r, "/upgrade", http.StatusFound)
		return
	}

	// Рендерим шаблон pro_editor
	renderTemplate(w, "pro_editor", map[string]interface{}{
		"User": user,
	})
}
