# frozen_string_literal: true

# Cluster mode as production runs it: the application boots in the master,
# which forks two workers.
workers 2
threads 1, 4
preload_app!
bind "tcp://127.0.0.1:#{ENV.fetch("PORT", "3000")}"
environment ENV.fetch("RAILS_ENV", "development")
