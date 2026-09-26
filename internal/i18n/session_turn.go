package i18n

// Texts of the turn summary, the agents panel and the usage commands of
// package session.
func init() {
	register(map[string]Text{
		// turn.go
		"session.turn.snapshot_failed": {EN: "⚠️ Could not take a git snapshot of the project; the turn summary will have no stats or buttons: <code>%s</code>", RU: "⚠️ Не удалось снять git-снимок проекта, сводка хода будет без статистики и кнопок: <code>%s</code>"},
		"session.turn.stat_gone":       {EN: " (deleted)", RU: " (удалён)"},
		"session.turn.stat_binary":     {EN: " (binary)", RU: " (бинарный)"},
		"session.turn.stat_new":        {EN: " +%d (new)", RU: " +%d (новый)"},
		"session.turn.btn_commit":      {EN: "✅ Commit", RU: "✅ Коммит"},
		"session.turn.btn_rollback":    {EN: "↩ Roll back", RU: "↩ Откатить"},
		"session.turn.btn_yes":         {EN: "Yes, roll back", RU: "Да, откатить"},
		"session.turn.btn_no":          {EN: "No", RU: "Нет"},
		"session.turn.no_diff_files":   {EN: "No files to diff", RU: "Нет файлов для diff"},
		"session.turn.diff_caption":    {EN: "🔀 Turn changes", RU: "🔀 Изменения хода"},
		"session.turn.rollback_cancel": {EN: "Rollback canceled.", RU: "Откат отменён."},
		"session.turn.busy":            {EN: "The agent is busy — wait for the turn to end", RU: "Агент работает — дождись конца хода"},
		"session.turn.commit_sent":     {EN: "Commit already requested", RU: "Коммит уже отправлен"},
		"session.turn.commit_queued":   {EN: "Asked the agent to commit", RU: "Задача на коммит отправлена"},
		"session.turn.n.rollback_ask":  {EN: "↩ Roll back %d file to the start of the turn? New files will be deleted.|↩ Roll back %d files to the start of the turn? New files will be deleted.", RU: "↩ Откатить %d файл к началу хода? Новые файлы будут удалены.|↩ Откатить %d файла к началу хода? Новые файлы будут удалены.|↩ Откатить %d файлов к началу хода? Новые файлы будут удалены."},
		"session.turn.n.rolled_back":   {EN: "↩ Rolled back %d file|↩ Rolled back %d files", RU: "↩ Откачен %d файл|↩ Откачено %d файла|↩ Откачено %d файлов"},
		"session.turn.kept_changed":    {EN: "skipped, edited after the turn", RU: "не тронуты, изменены после хода"},
		"session.turn.kept_new":        {EN: "new files kept, .gitignore changed during the turn", RU: "оставлены новые файлы, в ходе менялся .gitignore"},
		"session.turn.kept_ignored":    {EN: "skipped, ignored by git", RU: "пропущены, git их игнорирует"},

		// agents.go
		"session.agents.other_subagent": {EN: "another subagent", RU: "другой субагент"},
		"session.agents.btn_back":       {EN: "⬅ Back", RU: "⬅ Назад"},
		"session.agents.btn_result":     {EN: "📄 Result", RU: "📄 Результат"},
		"session.agents.already_done":   {EN: "The agent has already finished", RU: "Агент уже завершён"},
		"session.agents.process_gone":   {EN: "The agent process has already exited", RU: "Процесс агента уже остановлен"},
		"session.agents.stopping":       {EN: "Stopping…", RU: "Останавливаю"},
		"session.agents.no_result":      {EN: "No result available", RU: "Результат недоступен"},
		"session.agents.none":           {EN: "🤖 No agents in this session yet.", RU: "🤖 Агентов в этой сессии ещё не было."},
		"session.agents.continued":      {EN: "🤖 continuing after agent %s", RU: "🤖 продолжение после агента %s"},
		"session.agents.continued_bg":   {EN: "🤖 continuing after a background agent", RU: "🤖 продолжение после агента в фоне"},

		// usage.go
		"session.usage.btn_compact":    {EN: "🗜 Compact", RU: "🗜 Сжать"},
		"session.usage.hint":           {EN: "💡 Context at %d%% — compact the history before the agent does it automatically", RU: "💡 Контекст %d%% — сожми историю, пока агент не сделал это сам"},
		"session.usage.no_context":     {EN: "🧠 No context data yet — it appears after the first turn.", RU: "🧠 Данных о контексте пока нет — они появятся после первого хода."},
		"session.usage.compacted":      {EN: "🗜 History compacted (manual)", RU: "🗜 История сжата (вручную)"},
		"session.usage.compacted_auto": {EN: "🗜 History compacted (auto)", RU: "🗜 История сжата (авто)"},
		"session.usage.compacted_was":  {EN: ", was %s", RU: ", было %s"},
		"session.usage.already_queued": {EN: "Compaction already queued", RU: "Сжатие уже в очереди"},
		"session.usage.queued":         {EN: "Compaction queued", RU: "Сжатие в очереди"},
		"session.usage.compacting":     {EN: "Compacting…", RU: "Сжимаю…"},
	})
}
