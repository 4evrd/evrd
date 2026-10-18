// Conventional Commits (https://www.conventionalcommits.org): semantic-release
// reads them to pick the next version — feat: minor, fix/perf: patch,
// "BREAKING CHANGE:" (or feat!:) major.
export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'body-max-line-length': [0],
    'footer-max-line-length': [0],
  },
  // Dependabot writes "fix(deps): Bump …" — the prefix is ours (see
  // .github/dependabot.yml), the capital B isn't.
  ignores: [(msg) => msg.includes('Signed-off-by: dependabot[bot]')],
}
