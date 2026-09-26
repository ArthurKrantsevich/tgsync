// Package brand holds the bot's profile: avatar and descriptions, applied
// with `tgsync profile`. Regenerate the avatar with scripts/avatar.py.
package brand

import _ "embed"

//go:embed avatar.png
var Avatar []byte

// ShortDescription is shown on the bot's profile page (up to 120 characters).
const ShortDescription = "Пульт для Claude Code: задачи, ход работы, вопросы и разрешения агента — прямо из Telegram."

// Description is shown in an empty chat with the bot (up to 512 characters).
const Description = `tgsync — пульт для Claude Code на твоём компьютере.

• Запускай сессии в проектах и следи за ходом работы
• Отвечай на вопросы агента и выдавай разрешения кнопками
• Получай файлы, которые он пишет, и отправляй свои

Бот работает в группе с темами: у каждой сессии своя тема. Начни с /menu в теме ноды.`

// GroupDescription is set on the group when it has none (up to 255 characters).
const GroupDescription = "Пульт Claude Code: у каждой сессии своя тема. Начни с /menu в теме ноды 🖥, новую сессию открывает /new проект задача."
