# OK v4 Plugin Build System

PLUGIN_DIR = plugins
PLUGINS = ok-git ok-web-fetch ok-digest ok-utils ok-workflow ok-repo ok-debug \
          ok-browser ok-ai-vision ok-translate ok-ocr ok-database \
          ok-voice ok-desktop ok-computer-use ok-deploy ok-wake-word

.PHONY: all clean $(PLUGINS) register registry-server

all: $(PLUGINS)

$(PLUGINS):
	go build -o $(PLUGIN_DIR)/$@/$@-mcp ./$(PLUGIN_DIR)/$@/

clean:
	$(foreach p,$(PLUGINS),rm -f $(PLUGIN_DIR)/$p/$p-mcp$(EXE);)

ok-git:        ; go build -o $(PLUGIN_DIR)/ok-git/ok-git-mcp$(EXE) ./$(PLUGIN_DIR)/ok-git/
ok-web-fetch:  ; go build -o $(PLUGIN_DIR)/ok-web-fetch/ok-web-fetch-mcp$(EXE) ./$(PLUGIN_DIR)/ok-web-fetch/
ok-digest:     ; go build -o $(PLUGIN_DIR)/ok-digest/ok-digest-mcp$(EXE) ./$(PLUGIN_DIR)/ok-digest/
ok-utils:      ; go build -o $(PLUGIN_DIR)/ok-utils/ok-utils-mcp$(EXE) ./$(PLUGIN_DIR)/ok-utils/
ok-workflow:   ; go build -o $(PLUGIN_DIR)/ok-workflow/ok-workflow-mcp$(EXE) ./$(PLUGIN_DIR)/ok-workflow/
ok-repo:       ; go build -o $(PLUGIN_DIR)/ok-repo/ok-repo-mcp$(EXE) ./$(PLUGIN_DIR)/ok-repo/
ok-debug:      ; go build -o $(PLUGIN_DIR)/ok-debug/ok-debug-mcp$(EXE) ./$(PLUGIN_DIR)/ok-debug/
ok-browser:    ; go build -o $(PLUGIN_DIR)/ok-browser/ok-browser-mcp$(EXE) ./$(PLUGIN_DIR)/ok-browser/
ok-ai-vision:  ; go build -o $(PLUGIN_DIR)/ok-ai-vision/ok-ai-vision-mcp$(EXE) ./$(PLUGIN_DIR)/ok-ai-vision/
ok-translate:  ; go build -o $(PLUGIN_DIR)/ok-translate/ok-translate-mcp$(EXE) ./$(PLUGIN_DIR)/ok-translate/
ok-ocr:        ; go build -o $(PLUGIN_DIR)/ok-ocr/ok-ocr-mcp$(EXE) ./$(PLUGIN_DIR)/ok-ocr/
ok-database:   ; go build -o $(PLUGIN_DIR)/ok-database/ok-database-mcp$(EXE) ./$(PLUGIN_DIR)/ok-database/
ok-voice:      ; go build -o $(PLUGIN_DIR)/ok-voice/ok-voice-mcp$(EXE) ./$(PLUGIN_DIR)/ok-voice/
ok-desktop:    ; go build -o $(PLUGIN_DIR)/ok-desktop/ok-desktop-mcp$(EXE) ./$(PLUGIN_DIR)/ok-desktop/
ok-computer-use: ; go build -o $(PLUGIN_DIR)/ok-computer-use/ok-computer-use-mcp$(EXE) ./$(PLUGIN_DIR)/ok-computer-use/
ok-deploy:     ; go build -o $(PLUGIN_DIR)/ok-deploy/ok-deploy-mcp$(EXE) ./$(PLUGIN_DIR)/ok-deploy/
ok-wake-word:  ; go build -o $(PLUGIN_DIR)/ok-wake-word/ok-wake-word-mcp$(EXE) ./$(PLUGIN_DIR)/ok-wake-word/

register:
	@echo "# Add these to ok.toml:"
	@for p in $(PLUGINS); do \
		echo ""; \
		echo "[[plugins]]"; \
		echo "name = \"ok-$$p\""; \
		echo "type = \"stdio\""; \
		echo "command = \"./plugins/$$p/$$p-mcp\""; \
	done

registry-server:
	go build -o $(PLUGIN_DIR)/registry-server/ok-registry ./$(PLUGIN_DIR)/registry-server/
