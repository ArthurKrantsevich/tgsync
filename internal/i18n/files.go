package i18n

// File checks and sizes, the node card and topic cleanup, subscription
// limit notices.
func init() {
	register(map[string]Text{
		// Package files: path checks, reads, sizes, turn snapshots.
		"files.outside":         {EN: "%s: file is outside the project folder", RU: "%s: файл вне папки проекта"},
		"files.check_path":      {EN: "%s: could not check the path", RU: "%s: не удалось проверить путь"},
		"files.link_outside":    {EN: "%s: link points outside the project", RU: "%s: ссылка ведёт за пределы проекта"},
		"files.protected":       {EN: "%s: tgsync's own files can't be sent", RU: "%s: служебный файл tgsync не отправляется"},
		"files.not_found":       {EN: "%s: file not found", RU: "%s: файл не найден"},
		"files.is_dir":          {EN: "%s: this is a folder", RU: "%s: это папка"},
		"files.not_regular_at":  {EN: "%s: not a regular file", RU: "%s: не обычный файл"},
		"files.too_big":         {EN: "%s: %d MB, over Telegram's 50 MB limit", RU: "%s: %d МБ, больше лимита Telegram 50 МБ"},
		"files.open_failed":     {EN: "could not open the file", RU: "не удалось открыть файл"},
		"files.stat_failed":     {EN: "could not check the file", RU: "не удалось проверить файл"},
		"files.not_regular":     {EN: "not a regular file", RU: "не обычный файл"},
		"files.swapped":         {EN: "the file was replaced after the check", RU: "файл подменили после проверки"},
		"files.over_limit":      {EN: "over Telegram's 50 MB limit", RU: "больше лимита Telegram 50 МБ"},
		"files.diff_too_big":    {EN: "diff is over 50 MB", RU: "diff больше 50 МБ"},
		"files.dir_outside":     {EN: "%s: folder is outside the project", RU: "%s: папка вне проекта"},
		"files.dir_not_found":   {EN: "%s: folder not found", RU: "%s: папка не найдена"},
		"files.size.b":          {EN: "%d B", RU: "%d Б"},
		"files.size.kb":         {EN: "%d KB", RU: "%d КБ"},
		"files.size.mb":         {EN: "%.1f MB", RU: "%.1f МБ"},
		"files.no_turn_changes": {EN: "no changes in this turn", RU: "нет изменений за ход"},
		"files.no_git":          {EN: "the project is not in a git repository", RU: "проект не в git-репозитории"},

		// Package group: node card, admin rights, cleanup, sweep.
		"group.btn.cleanup":     {EN: "🧹 Clean up", RU: "🧹 Уборка"},
		"group.btn.sweep":       {EN: "🧽 Clear topic", RU: "🧽 Очистить тему"},
		"group.btn.delete":      {EN: "🗑 Delete", RU: "🗑 Удалить"},
		"group.btn.cancel":      {EN: "Cancel", RU: "Отмена"},
		"group.card":            {EN: "🖥 <b>%s</b>\nOpen sessions: %d · closed: %d", RU: "🖥 <b>%s</b>\nОткрытых сессий: %d · закрытых: %d"},
		"group.card.online":     {EN: "\n🟢 online since %s", RU: "\n🟢 онлайн с %s"},
		"group.rights.notice":   {EN: "🔧 The bot is missing admin rights. Without them, these don't work:\n%s", RU: "🔧 Боту не хватает прав администратора. Без них не работает:\n%s"},
		"group.rights.pin":      {EN: "• “Pin messages” — the pinned node card", RU: "• «Закрепление сообщений» — закреплённая карточка ноды"},
		"group.rights.info":     {EN: "• “Change group info” — group avatar and description", RU: "• «Изменение профиля группы» — аватар и описание группы"},
		"group.rights.delete":   {EN: "• “Delete messages” — deleting empty topics and 🧹 Clean up", RU: "• «Удаление сообщений» — удаление пустых тем и 🧹 Уборка"},
		"group.cleanup.nothing": {EN: "Nothing to clean up", RU: "Убирать нечего"},
		"group.cleanup.n.ask": {EN: "🧹 <b>Delete %d topic?</b> Its history will be lost.|🧹 <b>Delete %d topics?</b> Their history will be lost.",
			RU: "🧹 <b>Удалить %d тему?</b> История в ней пропадёт.|🧹 <b>Удалить %d темы?</b> История в них пропадёт.|🧹 <b>Удалить %d тем?</b> История в них пропадёт."},
		"group.cleanup.more":         {EN: "…and %d more", RU: "… и ещё %d"},
		"group.cleanup.crashed":      {EN: "crashed without a reply", RU: "упала без ответа"},
		"group.cleanup.closed_today": {EN: "closed today", RU: "закрыта сегодня"},
		"group.cleanup.n.closed_days": {EN: "closed %d day ago|closed %d days ago",
			RU: "закрыта %d дн. назад|закрыта %d дн. назад|закрыта %d дн. назад"},
		"group.cleanup.n.done": {EN: "🧹 Deleted %d topic.|🧹 Deleted %d topics.",
			RU: "🧹 Удалена %d тема.|🧹 Удалено %d темы.|🧹 Удалено %d тем."},
		"group.cleanup.partial":  {EN: "🧹 Deleted %d of %d. Errors:\n%s", RU: "🧹 Удалено %d из %d. Ошибки:\n%s"},
		"group.cleanup.canceled": {EN: "🧹 Cleanup canceled.", RU: "🧹 Уборка отменена."},
		"group.sweep.done":       {EN: "🧽 Messages deleted: %d", RU: "🧽 Удалено сообщений: %d"},
		"group.sweep.nothing":    {EN: "Nothing to clear", RU: "Чистить нечего"},

		// Package limits: subscription rate limit notices.
		"limits.warn":             {EN: "⚠️ Subscription limit “%s” is almost used up", RU: "⚠️ Лимит подписки «%s» почти исчерпан"},
		"limits.warn_pct":         {EN: "⚠️ Subscription limit “%s”: %d%%", RU: "⚠️ Лимит подписки «%s»: %d%%"},
		"limits.rejected":         {EN: "⛔ Subscription limit “%s” is used up", RU: "⛔ Лимит подписки «%s» исчерпан"},
		"limits.recovered":        {EN: "✅ Subscription limit “%s” is available again", RU: "✅ Лимит «%s» снова доступен"},
		"limits.warn_unnamed":     {EN: "⚠️ Subscription limit is almost used up", RU: "⚠️ Лимит подписки почти исчерпан"},
		"limits.rejected_unnamed": {EN: "⛔ Subscription limit is used up", RU: "⛔ Лимит подписки исчерпан"},
		"limits.reset":            {EN: " · resets %s", RU: " · сброс %s"},
	})
}
