.PHONY: changelog
changelog: $(GIT_CHGLOG)
	@echo "Generating changelog..."
	@./script/genchangelog.sh "$(RELEASE_TAG)" CHANGELOG.md

.PHONY: changelog-next
changelog-next: $(GIT_CHGLOG)
	@echo "Generating next version changelog..."
	@./script/genchangelog.sh "$(shell $(SEMVER) bump patch $(RELEASE_TAG))" CHANGELOG.md

.PHONY: changelog-patch
changelog-patch: $(GIT_CHGLOG)
	@echo "Generating patch version changelog..."
	@./script/genchangelog.sh "$(shell $(SEMVER) bump patch $(RELEASE_TAG))" CHANGELOG.md

.PHONY: changelog-minor
changelog-minor: $(GIT_CHGLOG)
	@echo "Generating minor version changelog..."
	@./script/genchangelog.sh "$(shell $(SEMVER) bump minor $(RELEASE_TAG))" CHANGELOG.md

.PHONY: changelog-major
changelog-major: $(GIT_CHGLOG)
	@echo "Generating major version changelog..."
	@./script/genchangelog.sh "$(shell $(SEMVER) bump major $(RELEASE_TAG))" CHANGELOG.md 