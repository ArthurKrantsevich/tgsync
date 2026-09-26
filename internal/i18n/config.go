package i18n

// Configuration errors, printed by tgsync check and on startup.
func init() {
	register(map[string]Text{
		"config.home_missing":  {EN: "TGSYNC_HOME=%s: directory does not exist", RU: "TGSYNC_HOME=%s: папки не существует"},
		"config.required":      {EN: "%s is required", RU: "%s не задана"},
		"config.group_id":      {EN: "GROUP_CHAT_ID must be a negative supergroup id like -1001234567890", RU: "GROUP_CHAT_ID должен быть отрицательным id супергруппы, например -1001234567890"},
		"config.abs_path":      {EN: "%s must be an absolute path", RU: "%s должен быть абсолютным путём"},
		"config.positive_int":  {EN: "%s must be a positive integer", RU: "%s должно быть целым числом больше нуля"},
		"config.sudo_password": {EN: "SUDO_MODE=env requires SUDO_PASSWORD", RU: "SUDO_MODE=env требует SUDO_PASSWORD"},
		"config.sudo_mode":     {EN: "SUDO_MODE must be off, env or telegram", RU: "SUDO_MODE должен быть off, env или telegram"},
		"config.duration":      {EN: "%s must be a duration like 30m or 2h, got %q", RU: "%s должно быть длительностью вроде 30m или 2h, а не %q"},
		"config.bool":          {EN: "%s must be true or false", RU: "%s должно быть true или false"},
		"config.stt_url":       {EN: "STT_URL must be an http(s) URL like http://127.0.0.1:8000", RU: "STT_URL должен быть http(s)-адресом, например http://127.0.0.1:8000"},
		"config.language":      {EN: "BOT_LANGUAGE must be en or ru, got %q", RU: "BOT_LANGUAGE должен быть en или ru, а не %q"},
		"config.user_id":       {EN: "invalid user id %q", RU: "неверный id пользователя %q"},
	})
}
