include $(abspath $(CURDIR)/../../make/init.mk)

AP_RUN_NAME := $(notdir $(CURDIR))
AP_RUN_DIR  := $(DEVCACHE_RUN)/$(AP_RUN_NAME)

ifneq ($(AKASH_HOME),)
ifneq ($(DIRENV_FILE),$(CURDIR)/.envrc)
$(error "AKASH_HOME is set by the upper dir (probably in ~/.bashrc|~/.zshrc), \
but direnv does not seem to be configured. \
Ensure direnv is installed and hooked to your shell profile. Refer to the documentation for details. \
")
endif
else
export AKASH_HOME = $(DEVCACHE_RUN)/$(AP_RUN_NAME)/.akash
endif

.PHONY: bins
bins:
ifneq ($(SKIP_BUILD), true)
	make -C $(AP_ROOT) bins
endif
