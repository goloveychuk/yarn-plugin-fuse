yarn plugin import https://github.com/goloveychuk/yarn-plugin-ignore-deps/releases/latest/download/plugin-ignore-deps.js


Example usage:

in `.yarnrc.yml`:
```yaml
ignoreDependencies:
  depsOf:
    - 'some-monorepo-package/*'
    - 'lodash'
```