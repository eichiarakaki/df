DIST := dist

.PHONY: build clean install uninstall

build:
	mkdir -p $(DIST)
	go build -o $(DIST)/df ./cmd/df
	@echo "Built to $(DIST)/"

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(DIST)/df $(HOME)/.local/bin/df
	@echo "Installed binary to ~/.local/bin/"

	mkdir -p $(HOME)/.config/df
	@if [ ! -f "$(HOME)/.config/df/df.yaml" ]; then \
		cp config/df.yaml $(HOME)/.config/df/df.yaml; \
		echo "Created ~/.config/df/df.yaml"; \
	else \
		echo "Skipped ~/.config/df/df.yaml (already exists)"; \
	fi

uninstall:
	rm -f $(HOME)/.local/bin/df
	@echo "Removed binary from ~/.local/bin/"
	@printf "Remove ~/.config/df/df.yaml? [y/N] " && read ans && [ "$$ans" = "y" ] && rm -rf $(HOME)/.config/df && echo "Removed ~/.config/df/" || echo "Config kept."

clean:
	rm -rf $(DIST)
