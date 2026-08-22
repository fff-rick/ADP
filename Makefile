COMPOSE_FILE := deploy/docker-compose/compose.yml
ENV_FILE := deploy/docker-compose/.env
COMPOSE := docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE)

.DEFAULT_GOAL := help
.PHONY: help init build up down restart logs ps config clean

help: ## Show available Docker development commands.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "%-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

init: ## Create the ignored local Compose environment file.
	@test ! -f $(ENV_FILE) || { echo "$(ENV_FILE) already exists"; exit 0; }
	cp deploy/docker-compose/.env.example $(ENV_FILE)
	@echo "Created $(ENV_FILE); replace the development secrets before sharing this stack."

build: ## Build the server and Worker images.
	@$(MAKE) ensure-env
	$(COMPOSE) build

up: ## Start PostgreSQL, ADP server, and Worker in the background.
	@$(MAKE) ensure-env
	$(COMPOSE) up --build -d

down: ## Stop the stack without deleting the PostgreSQL volume.
	@$(MAKE) ensure-env
	$(COMPOSE) down

restart: ## Restart all services.
	@$(MAKE) ensure-env
	$(COMPOSE) restart

logs: ## Follow logs for all services (SERVICE=server limits output to one service).
	@$(MAKE) ensure-env
	$(COMPOSE) logs -f $(SERVICE)

ps: ## Show service status.
	@$(MAKE) ensure-env
	$(COMPOSE) ps

config: ## Render and validate the resolved Compose configuration.
	@$(MAKE) ensure-env
	$(COMPOSE) config --no-interpolate

clean: ## Stop services and remove the local PostgreSQL data volume.
	@$(MAKE) ensure-env
	$(COMPOSE) down --volumes --remove-orphans

ensure-env:
	@test -f $(ENV_FILE) || { echo "Missing $(ENV_FILE). Run 'make init' first."; exit 1; }
