package i18n

// Texts of package render: status, usage, context and agents panels.
func init() {
	register(map[string]Text{
		// status.go
		"render.tool.todo":        {EN: "🗒 Task plan updated", RU: "🗒 План задач обновлён"},
		"render.tool.ask":         {EN: "❓ Question for the user", RU: "❓ Вопрос пользователю"},
		"render.status.head":      {EN: "%s %s · step %d · last event %s ago", RU: "%s %s · шаг %d · последнее событие %s назад"},
		"render.result.error":     {EN: "⚠️ Turn failed · %s", RU: "⚠️ Ход завершился с ошибкой · %s"},
		"render.result.done":      {EN: "✅ Turn done", RU: "✅ Ход завершён"},
		"render.result.steps":     {EN: "steps: %d", RU: "шагов: %d"},
		"render.result.interrupt": {EN: "⏹ Turn interrupted · %s · steps: %d", RU: "⏹ Ход прерван · %s · шагов: %d"},

		// usage.go: reset times and limit windows
		"render.reset.today":             {EN: "at %s", RU: "в %s"},
		"render.weekday.0":               {EN: "Sun", RU: "вс"},
		"render.weekday.1":               {EN: "Mon", RU: "пн"},
		"render.weekday.2":               {EN: "Tue", RU: "вт"},
		"render.weekday.3":               {EN: "Wed", RU: "ср"},
		"render.weekday.4":               {EN: "Thu", RU: "чт"},
		"render.weekday.5":               {EN: "Fri", RU: "пт"},
		"render.weekday.6":               {EN: "Sat", RU: "сб"},
		"render.window.five_hour":        {EN: "5 hours", RU: "5 часов"},
		"render.window.seven_day":        {EN: "7 days", RU: "7 дней"},
		"render.window.seven_day_opus":   {EN: "7 days Opus", RU: "7 дней Opus"},
		"render.window.seven_day_sonnet": {EN: "7 days Sonnet", RU: "7 дней Sonnet"},
		"render.window.overage":          {EN: "overage", RU: "сверх лимита"},
		"render.window.subscription":     {EN: "subscription", RU: "подписка"},
		"render.limit.reset":             {EN: "%s: reset", RU: "%s: сброшен"},
		"render.limit.nodata":            {EN: "%s: no data", RU: "%s: нет данных"},
		"render.limit.resets":            {EN: " · resets %s", RU: " · сброс %s"},

		// usage.go: /context
		"render.ctx.head":              {EN: "🧠 <b>Context</b>", RU: "🧠 <b>Контекст</b>"},
		"render.ctx.cat.messages":      {EN: "messages", RU: "сообщения"},
		"render.ctx.cat.system_prompt": {EN: "system prompt", RU: "системный промпт"},
		"render.ctx.cat.system_tools":  {EN: "tools", RU: "инструменты"},
		"render.ctx.cat.mcp_tools":     {EN: "MCP", RU: "MCP"},
		"render.ctx.cat.memory_files":  {EN: "memory", RU: "память"},
		"render.ctx.cat.custom_agents": {EN: "agents", RU: "агенты"},
		"render.ctx.cat.skills":        {EN: "skills", RU: "skills"},
		"render.ctx.auto_off":          {EN: "Auto-compact: off", RU: "Авто-сжатие: выкл"},
		"render.ctx.auto_on":           {EN: "Auto-compact: on", RU: "Авто-сжатие: вкл"},
		"render.ctx.auto_at":           {EN: ", at %d%%", RU: ", при %d%%"},
		"render.ctx.stale":             {EN: "<i>(as of the end of the turn at %s; %s)</i>", RU: "<i>(на конец хода %s, %s)</i>"},
		"render.ctx.stale_down":        {EN: "the process is not running now", RU: "процесс сейчас не запущен"},
		"render.ctx.stale_silent":      {EN: "the process did not answer", RU: "процесс не ответил"},

		// usage.go: /usage
		"render.usage.nodata":    {EN: "📊 Subscription: no data yet, it comes with the next turn.", RU: "📊 Подписка: данных нет — они приходят вместе с ходами."},
		"render.usage.head":      {EN: "📊 <b>Subscription</b>", RU: "📊 <b>Подписка</b>"},
		"render.usage.today":     {EN: "Today", RU: "Сегодня"},
		"render.usage.week":      {EN: "7 days", RU: "7 дней"},
		"render.usage.footnote":  {EN: "<i>≈ is the CLI's estimate at API prices; a subscription is not charged for it.</i>", RU: "<i>≈ — оценка CLI по ценам API; на подписке деньги не списываются.</i>"},
		"render.usage.period":    {EN: "<b>%s</b>: %s tok. · ≈$%.2f", RU: "<b>%s</b>: %s ток. · ≈$%.2f"},
		"render.usage.others":    {EN: "  others %s · ≈$%.2f", RU: "  прочие %s · ≈$%.2f"},
		"render.usage.n.session": {EN: "📊 <b>This session</b>: %d turn · %s tok.|📊 <b>This session</b>: %d turns · %s tok.", RU: "📊 <b>Эта сессия</b>: ходов %d · %s ток.|📊 <b>Эта сессия</b>: ходов %d · %s ток.|📊 <b>Эта сессия</b>: ходов %d · %s ток."},
		"render.usage.cache":     {EN: " (+%s from cache)", RU: " (+%s из кеша)"},

		// agents.go
		"render.agents.head":           {EN: "🤖 <b>Agents</b> · %d %s · %d %s", RU: "🤖 <b>Агенты</b> · %d %s · %d %s"},
		"render.agents.n.running":      {EN: "running|running", RU: "работает|работают|работают"},
		"render.agents.n.done":         {EN: "done|done", RU: "готов|готово|готово"},
		"render.agents.n.more_running": {EN: "… %d more running|… %d more running", RU: "… ещё %d работает|… ещё %d работают|… ещё %d работают"},
		"render.agents.n.more_done":    {EN: "… %d more done|… %d more done", RU: "… ещё %d готовый|… ещё %d готовых|… ещё %d готовых"},
		"render.agents.main":           {EN: "main agent", RU: "основной агент"},
		"render.agents.caller":         {EN: "Called by: %s", RU: "Вызвал: %s"},
		"render.agents.n.tools":        {EN: " · %d tool| · %d tools", RU: " · %d инстр.| · %d инстр.| · %d инстр."},
		"render.agents.running_for":    {EN: "Running for %s", RU: "Работает %s"},
		"render.agents.now":            {EN: "Now: %s", RU: "Сейчас: %s"},
		"render.agents.took.completed": {EN: "Done in %s", RU: "Готово за %s"},
		"render.agents.took.failed":    {EN: "Failed after %s", RU: "Ошибка за %s"},
		"render.agents.took.stopped":   {EN: "Stopped after %s", RU: "Остановлен за %s"},
		"render.agents.took.other":     {EN: "Finished in %s", RU: "Завершён за %s"},
	})
}
