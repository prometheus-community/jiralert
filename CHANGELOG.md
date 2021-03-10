# Changelog
All notable changes to this project will be documented in this file.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
Helper to list all merge commits between a release and HEAD: `git log --oneline --reverse 1.0...HEAD`

## Diffs
[Unreleased]: https://github.com/prometheus-community/jiralert/compare/1.0...HEAD
[1.0]: https://github.com/prometheus-community/jiralert/compare/0.6...1.0
[0.6]: https://github.com/prometheus-community/jiralert/compare/0.5...0.6
[0.5]: https://github.com/prometheus-community/jiralert/compare/0.4...0.5
[0.4]: https://github.com/prometheus-community/jiralert/compare/0.3...0.4
[0.3]: https://github.com/prometheus-community/jiralert/compare/0.2...0.3
[0.2]: https://github.com/prometheus-community/jiralert/compare/0.1...0.2
[0.1]: https://github.com/prometheus-community/jiralert/releases/tag/0.1

## [1.1] - Unreleased
### Added
- Jiralert docker image [#22](https://github.com/prometheus-community/jiralert/pull/22)
- Slack button in Readme [#23](https://github.com/prometheus-community/jiralert/pull/23)
  - Migrate to github actions [#81](https://github.com/prometheus-community/jiralert/pull/81)
  - Add CircleCI [#33](https://github.com/prometheus-community/jiralert/pull/33
  - Update circleci/golang version from 1.12 to 1.14 [#76](https://github.com/prometheus-community/jiralert/pull/76)
- Adds match and stringSlice to template functions [#55](https://github.com/prometheus-community/jiralert/pull/55)
- Add ReopenDuration handling: otherwise Jira issue is never reopened [#61](https://github.com/prometheus-community/jiralert/pull/61)

### Changed
- Migrate from dep to go modules [#24](https://github.com/prometheus-community/jiralert/pull/24)
- Split project into separate go packages [#27](https://github.com/prometheus-community/jiralert/pull/27)
- Switch logging from glog to go-kit/log [#31](https://github.com/prometheus-community/jiralert/pull/31)
- Use Docker multistage builds [#44](https://github.com/prometheus-community/jiralert/pull/44)
- Synchronize Makefile.common from prometheus/prometheus [#60](https://github.com/prometheus-community/jiralert/pull/60)
- Synchronize common files from prometheus/prometheus [#63](https://github.com/prometheus-community/jiralert/pull/63)
- Update common Prometheus files [#71](https://github.com/prometheus-community/jiralert/pull/71)
- New opt-in label hashing behavior behind `-hash-jira-label` [#79](https://github.com/prometheus-community/jiralert/pull/79)
  - **next release will drop the flag and promote this to default behavior**

### Fixed
- Fix sample configuration file linting issues [#68](https://github.com/prometheus-community/jiralert/pull/68)
- Update the description field on open issues [#75](https://github.com/prometheus-community/jiralert/pull/75)
- error if http method for home or config is not GET [#78](https://github.com/prometheus-community/jiralert/pull/78)

### Removed
- Remove -ignore flag from Makefile, fix log.level info [#42](https://github.com/prometheus-community/jiralert/pull/42)
