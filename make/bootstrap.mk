setup-tools:
	@echo "Installing required tools for profile: $(PROFILE)..."
	@bash bootstrap/setup-tools.sh $(PROFILE)