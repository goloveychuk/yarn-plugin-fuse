import {
  Hooks,
  Plugin,
  structUtils,
  Resolver,
  ResolveOptions,
  MinimalResolveOptions,
  Locator,
  SettingsType,
  Package,
  Descriptor,
  FetchOptions,
  Fetcher,
  miscUtils,
  DescriptorHash,
} from '@yarnpkg/core';
// import { PortablePath, xfs, NoFS, JailFS } from '@yarnpkg/fslib';
import { parseResolution, Resolution } from '@yarnpkg/parsers';

const PROTOCOL = `ignoreDeps:`;

function getOriginalDescriptor(desc: Descriptor) {
  const { source } = structUtils.parseRange(desc.range);
  return structUtils.parseDescriptor(source);
}

function addProtocolToDescriptor(desc: Descriptor) {
  return structUtils.makeDescriptor(
    desc,
    structUtils.makeRange({
      protocol: PROTOCOL,
      source: structUtils.stringifyDescriptor(desc),
      selector: '',
      params: null,
    }),
  );
}

function getOriginalLocator(loc: Locator) {
  const { source } = structUtils.parseRange(loc.reference);
  return structUtils.parseLocator(source);
}

function addProtocolToLocator(loc: Locator) {
  return structUtils.makeLocator(
    loc,
    structUtils.makeRange({
      protocol: PROTOCOL,
      source: structUtils.stringifyLocator(loc),
      selector: '',
      params: null,
    }),
  );
}

class IgnoreDepsResolver implements Resolver {
  supportsDescriptor(descriptor: Descriptor, opts: MinimalResolveOptions) {
    if (!descriptor.range.startsWith(PROTOCOL)) return false;
    return true;
  }

  supportsLocator(locator: Locator, opts: MinimalResolveOptions) {
    if (!locator.reference.startsWith(PROTOCOL)) return false;
    return true;
  }

  shouldPersistResolution(locator: Locator, opts: MinimalResolveOptions) {
    return opts.resolver.shouldPersistResolution(
      getOriginalLocator(locator),
      opts,
    );
  }

  bindDescriptor(
    _descriptor: Descriptor,
    fromLocator: Locator,
    opts: MinimalResolveOptions,
  ) {
    const descriptor = getOriginalDescriptor(_descriptor);
    return addProtocolToDescriptor(
      opts.resolver.bindDescriptor(descriptor, fromLocator, opts),
    );
  }

  getResolutionDependencies(
    //todo check docs!!!
    _descriptor: Descriptor,
    opts: MinimalResolveOptions,
  ) {
    const descriptor = getOriginalDescriptor(_descriptor);
    return opts.resolver.getResolutionDependencies(descriptor, opts);
  }

  async getCandidates(
    _descriptor: Descriptor,
    dependencies: Map<DescriptorHash, Package>,
    opts: ResolveOptions,
  ) {
    const descriptor = getOriginalDescriptor(_descriptor);
    return (
      await opts.resolver.getCandidates(descriptor, dependencies, opts)
    ).map(addProtocolToLocator);
  }

  async getSatisfying(
    _descriptor: Descriptor,
    references: Array<string>,
    opts: ResolveOptions,
  ) {
    const descriptor = getOriginalDescriptor(_descriptor);
    return opts.resolver.getSatisfying(descriptor, references, opts);
  }

  async resolve(_locator: Locator, opts: ResolveOptions): Promise<Package> {
    const locator = getOriginalLocator(_locator);
    const sourcePkg = await opts.resolver.resolve(locator, opts);
    return {
      ...sourcePkg,
      ..._locator,
      peerDependencies: new Map(),
      dependencies: new Map(),
    };
  }
}

class IgnoreDepsFetcher implements Fetcher {
  supports(locator: Locator) {
    if (locator.reference.startsWith(PROTOCOL)) return true;

    return false;
  }

  getLocalPath(locator: Locator, opts: FetchOptions) {
    return opts.fetcher.getLocalPath(getOriginalLocator(locator), opts);
  }

  async fetch(locator: Locator, opts: FetchOptions) {
    return opts.fetcher.fetch(getOriginalLocator(locator), opts);
    // const tempDir = await xfs.mktempPromise();
    // const p = tempDir as PortablePath;
    // return {
    //   packageFs: new JailFS(p),
    //   prefixPath: PortablePath.dot,
    // };
  }
}

function isMatched(
  pattern: Resolution,
  dependency: Descriptor,
  locator: Locator,
) {
  if (pattern.from) {
    if (pattern.from.fullName !== '*') {
      if (pattern.from.fullName !== structUtils.stringifyIdent(locator)) {
        return false;
      }
    }
  }
  if (pattern.descriptor.fullName === '*') {
    return true;
  }
  return pattern.descriptor.fullName === structUtils.stringifyIdent(dependency);
}
const memo = (() => {
  const map = new WeakMap();
  return <K extends object, T extends object>(k: K, fn: (k: K) => T): T => {
    if (!map.has(k)) {
      map.set(k, fn(k));
    }
    return map.get(k);
  };
})();

const hooks: Hooks = {
  async reduceDependency(
    dependency,
    project,
    locator, //parent
    initialDependency,
    extra,
  ) {
    const ignoreDepsOf = memo(
      project.configuration.get('ignoreDependencies').get('depsOf'),
      (deps) => deps.map(parseResolution),
    );

    for (const pattern of ignoreDepsOf) {
      // https://github.com/yarnpkg/berry/blob/bdd107579b76a71970a61c2cce46a5d4b51ca596/packages/yarnpkg-core/sources/CorePlugin.ts#L17
      if (isMatched(pattern, dependency, locator)) {
        return addProtocolToDescriptor(dependency);
      }
    }
    return dependency;
  },
};

declare module '@yarnpkg/core' {
  interface ConfigurationValueMap {
    ignoreDependencies: miscUtils.ToMapValue<{
      depsOf: Array<string>;
    }>;
  }
}

const plugin: Plugin = {
  configuration: {
    ignoreDependencies: {
      type: SettingsType.SHAPE,
      description: 'Ignore packages',
      properties: {
        depsOf: {
          description: ``,
          default: [],
          isArray: true,
          type: SettingsType.STRING,
        },
      },
    },
  },
  hooks,
  resolvers: [IgnoreDepsResolver],
  fetchers: [IgnoreDepsFetcher],
  commands: [],
};

export default plugin;
