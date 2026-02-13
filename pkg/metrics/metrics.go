package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// SenryuDetectedTotal is the total number of senryu detected
	SenryuDetectedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "findsenryu_senryu_detected_total",
			Help: "Total number of senryu detected",
		},
		[]string{"guild_id"},
	)

	// CommandsExecutedTotal is the total number of commands executed
	CommandsExecutedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "findsenryu_commands_executed_total",
			Help: "Total number of commands executed",
		},
		[]string{"command"},
	)

	// ConnectedGuilds is the current number of connected guilds
	ConnectedGuilds = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "findsenryu_connected_guilds",
			Help: "Current number of connected guilds",
		},
	)

	// ErrorsTotal is the total number of errors
	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "findsenryu_errors_total",
			Help: "Total number of errors",
		},
		[]string{"type"},
	)

	// MessagesProcessedTotal is the total number of messages processed
	MessagesProcessedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "findsenryu_messages_processed_total",
			Help: "Total number of messages processed",
		},
	)

	// DatabaseOperationsTotal is the total number of database operations
	DatabaseOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "findsenryu_database_operations_total",
			Help: "Total number of database operations",
		},
		[]string{"operation"},
	)

	// SenryuStoredTotal is the total number of senryu stored in database
	SenryuStoredTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "findsenryu_senryu_stored_total",
			Help: "Total number of senryu stored in database",
		},
	)

	// MutedChannelsTotal is the total number of muted channels
	MutedChannelsTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "findsenryu_muted_channels_total",
			Help: "Total number of muted channels",
		},
	)

	// DiscordLatency is the Discord API latency
	DiscordLatency = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "findsenryu_discord_latency_milliseconds",
			Help: "Discord API latency in milliseconds",
		},
	)
)

// RecordSenryuDetected records a senryu detection
func RecordSenryuDetected(guildID string) {
	SenryuDetectedTotal.WithLabelValues(guildID).Inc()
}

// RecordCommandExecuted records a command execution
func RecordCommandExecuted(command string) {
	CommandsExecutedTotal.WithLabelValues(command).Inc()
}

// SetConnectedGuilds sets the number of connected guilds
func SetConnectedGuilds(count int) {
	ConnectedGuilds.Set(float64(count))
}

// RecordError records an error
func RecordError(errType string) {
	ErrorsTotal.WithLabelValues(errType).Inc()
}

// RecordMessageProcessed records a processed message
func RecordMessageProcessed() {
	MessagesProcessedTotal.Inc()
}

// RecordDatabaseOperation records a database operation
func RecordDatabaseOperation(operation string) {
	DatabaseOperationsTotal.WithLabelValues(operation).Inc()
}

// SetDatabaseStats sets database statistics
func SetDatabaseStats(senryuCount, mutedChannelCount int64) {
	SenryuStoredTotal.Set(float64(senryuCount))
	MutedChannelsTotal.Set(float64(mutedChannelCount))
}

// SetDiscordLatency sets the Discord API latency
func SetDiscordLatency(latencyMs float64) {
	DiscordLatency.Set(latencyMs)
}
