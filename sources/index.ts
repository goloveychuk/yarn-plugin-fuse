import {
  structUtils,
  Report,
  Manifest,
  miscUtils,
  formatUtils,
  Plugin,
  Hooks,
  InstallPackageExtraApi,
  SettingsType,
} from '@yarnpkg/core';
import {
  Locator,
  Package,
  FinalizeInstallStatus,
  hashUtils,
} from '@yarnpkg/core';
import {
  Linker,
  LinkOptions,
  MinimalLinkOptions,
  LinkType,
  WindowsLinkType,
} from '@yarnpkg/core';
import {
  LocatorHash,
  Descriptor,
  DependencyMeta,
  Configuration,
} from '@yarnpkg/core';
import { MessageName, Project, FetchResult, Installer } from '@yarnpkg/core';
import {
  PortablePath,
  npath,
  ppath,
  Filename,
  AliasFS,
  CwdFS,
} from '@yarnpkg/fslib';
import { VirtualFS, xfs, FakeFS, NativePath } from '@yarnpkg/fslib';
import { ZipOpenFS } from '@yarnpkg/libzip';
import { buildNodeModulesTree } from '@yarnpkg/nm';
import {
  NodeModulesLocatorMap,
  buildLocatorMap,
  NodeModulesHoistingLimits,
} from '@yarnpkg/nm';
import { jsInstallUtils, pnpUtils } from '@yarnpkg/plugin-pnp';
import { PnpApi, PackageInformation } from '@yarnpkg/pnp';
import { FuseData, FuseNode } from './types';
import {runFuse} from './runFuse'

const NODE_MODULES = `node_modules` as Filename;

function getUnpluggedPath(
  locator: Locator,
  { configuration }: { configuration: Configuration },
) {
  return ppath.resolve(
    configuration.get(`pnpUnpluggedFolder2`),
    structUtils.slugifyLocator(locator),
  );
}

const FORCED_UNPLUG_PACKAGES = new Set([
  // Contains native binaries
  structUtils.makeIdent(null, `open`).identHash,
  structUtils.makeIdent(null, `opn`).identHash,
]);

type LocatorKey = string;

type BinSymlinkMap = Map<PortablePath, Map<Filename, PortablePath>>;

type UnboxPromise<T extends Promise<any>> = T extends Promise<infer U>
  ? U
  : never;
type CustomPackageData = UnboxPromise<
  ReturnType<typeof extractCustomPackageData>
>;

function isLinkLocator(locatorKey: LocatorKey): boolean {
  let descriptor = structUtils.parseDescriptor(locatorKey);
  if (structUtils.isVirtualDescriptor(descriptor))
    descriptor = structUtils.devirtualizeDescriptor(descriptor);

  return descriptor.range.startsWith(`link:`);
}

//todo copy getBuildHash
type LocationNode = {
  children: Map<Filename, LocationNode>;
  locator?: LocatorKey;
  linkType: LinkType;
  target?: PortablePath;
};
type LocationRoot = PortablePath;

/**
 * Locations tree. It starts with the map of location roots and continues as maps
 * of nested directory entries.
 *
 * Example:
 *  Map {
 *   '' => children: Map {
 *     'react-apollo' => {
 *       children: Map {
 *         'node_modules' => {
 *           children: Map {
 *             '@apollo' => {
 *               children: Map {
 *                 'react-hooks' => {
 *                   children: Map {},
 *                   locator: '@apollo/react-hooks:virtual:cf...#npm:3.1.3'
 *                 }
 *               }
 *             }
 *           }
 *         }
 *       },
 *       locator: 'react-apollo:virtual:24...#npm:3.1.3'
 *     },
 *   },
 *   'packages/client' => children: Map {
 *     'node_modules' => Map {
 *       ...
 *     }
 *   }
 *   ...
 * }
 */
type LocationTree = Map<LocationRoot, LocationNode>;

const parseLocation = (
  location: PortablePath,
  { skipPrefix }: { skipPrefix: PortablePath },
): { locationRoot: PortablePath; segments: Array<Filename> } => {
  const projectRelativePath = ppath.contains(skipPrefix, location);
  if (projectRelativePath === null)
    throw new Error(
      `Assertion failed: Writing attempt prevented to ${location} which is outside project root: ${skipPrefix}`,
    );

  const allSegments = projectRelativePath
    .split(ppath.sep)
    // Ignore empty segments (after trailing slashes)
    .filter((segment) => segment !== ``);
  const nmIndex = allSegments.indexOf(NODE_MODULES);

  // Project path, up until the first node_modules segment
  const relativeRoot = allSegments
    .slice(0, nmIndex)
    .join(ppath.sep) as PortablePath;
  const locationRoot = ppath.join(skipPrefix, relativeRoot);

  // All segments that follow
  const segments = allSegments.slice(nmIndex) as Array<Filename>;

  return { locationRoot, segments };
};

type LoadManifest = (
  locator: LocatorKey,
  installLocation: PortablePath,
) => Promise<Pick<Manifest, 'bin'>>;

const buildLocationTree = (
  locatorMap: NodeModulesLocatorMap | null,
  { skipPrefix }: { skipPrefix: PortablePath },
): LocationTree => {
  const locationTree: LocationTree = new Map();
  if (locatorMap === null) return locationTree;

  const makeNode: () => LocationNode = () => ({
    children: new Map(),
    linkType: LinkType.HARD,
  });

  for (const [locator, info] of locatorMap.entries()) {
    if (info.linkType === LinkType.SOFT) {
      const internalPath = ppath.contains(skipPrefix, info.target);
      if (internalPath !== null) {
        const node = miscUtils.getFactoryWithDefault(
          locationTree,
          info.target,
          makeNode,
        );
        node.locator = locator;
        node.linkType = info.linkType;
      }
    }

    for (const location of info.locations) {
      const { locationRoot, segments } = parseLocation(location, {
        skipPrefix,
      });

      let node = miscUtils.getFactoryWithDefault(
        locationTree,
        locationRoot,
        makeNode,
      );

      for (let idx = 0; idx < segments.length; ++idx) {
        const segment = segments[idx];
        // '.' segment exists only for top-level locator, skip it
        if (segment !== `.`) {
          const nextNode = miscUtils.getFactoryWithDefault(
            node.children,
            segment,
            makeNode,
          );

          node.children.set(segment, nextNode);
          node = nextNode;
        }

        if (idx === segments.length - 1) {
          node.locator = locator;
          node.linkType = info.linkType;
          node.target = info.target;
        }
      }
    }
  }

  return locationTree;
};

async function createBinSymlinkMap(
  installState: NodeModulesLocatorMap,
  locationTree: LocationTree,
  projectRoot: PortablePath,
  { loadManifest }: { loadManifest: LoadManifest },
) {
  const locatorScriptMap = new Map<LocatorKey, Map<string, string>>();
  for (const [locatorKey, { locations }] of installState) {
    const manifest = !isLinkLocator(locatorKey)
      ? await loadManifest(locatorKey, locations[0])
      : null;

    const bin = new Map();
    if (manifest) {
      for (const [name, value] of manifest.bin) {
        // const target = ppath.join(locations[0], value);
        if (value !== ``) {
          //&& xfs.existsSync(target)
          bin.set(name, value);
        }
      }
    }

    locatorScriptMap.set(locatorKey, bin);
  }

  const binSymlinks: BinSymlinkMap = new Map();

  const getBinSymlinks = (
    location: PortablePath,
    parentLocatorLocation: PortablePath,
    node: LocationNode,
  ): Map<Filename, PortablePath> => {
    const symlinks = new Map();
    const internalPath = ppath.contains(projectRoot, location);
    if (node.locator && internalPath !== null) {
      const binScripts = locatorScriptMap.get(node.locator)!;
      for (const [filename, scriptPath] of binScripts) {
        const symlinkTarget = ppath.join(
          location,
          npath.toPortablePath(scriptPath),
        );
        symlinks.set(filename, symlinkTarget);
      }
      for (const [childLocation, childNode] of node.children) {
        const absChildLocation = ppath.join(location, childLocation);
        const childSymlinks = getBinSymlinks(
          absChildLocation,
          absChildLocation,
          childNode,
        );
        if (childSymlinks.size > 0) {
          binSymlinks.set(
            location,
            new Map([
              ...(binSymlinks.get(location) || new Map()),
              ...childSymlinks,
            ]),
          );
        }
      }
    } else {
      for (const [childLocation, childNode] of node.children) {
        const childSymlinks = getBinSymlinks(
          ppath.join(location, childLocation),
          parentLocatorLocation,
          childNode,
        );
        for (const [name, symlinkTarget] of childSymlinks) {
          symlinks.set(name, symlinkTarget);
        }
      }
    }
    return symlinks;
  };

  for (const [location, node] of locationTree) {
    const symlinks = getBinSymlinks(location, location, node);
    if (symlinks.size > 0) {
      binSymlinks.set(
        location,
        new Map([...(binSymlinks.get(location) || new Map()), ...symlinks]),
      );
    }
  }

  return binSymlinks;
}

function buildFuseTree(
  locationTree: LocationTree,
  binSymlinks: BinSymlinkMap,
): FuseData {
  const result: FuseData = { roots: {} };

  const mapNode = (node: LocationNode): FuseNode => {
    const children = Array.from(node.children.entries()).map(
      ([name, child]) => [name, mapNode(child)] as const,
    );
    return {
      linkType: node.linkType,
      target: node.target,
      children: Object.fromEntries(children),
    };
  };

  for (const [key, value] of locationTree.entries()) {
    if (value.linkType === LinkType.SOFT) {
      continue;
    }
    for (const [chName, ch] of value.children.entries()) {
      const root = ppath.join(key, chName);
      const node = mapNode(ch);

      const bins = binSymlinks.get(key);
      if (bins) {
        const binDir = (node.children[`.bin`] ??= {
          children: {},
          linkType: LinkType.HARD,
        });
        for (const [binName, binTarget] of bins.entries()) {
          binDir.children[binName] = {
            linkType: LinkType.SOFT,
            target: binTarget,
          };
        }
      }

      result.roots[root] = node;
    }
  }
  return result;
}

async function extractCustomPackageData(
  pkg: Package,
  fetchResult: FetchResult,
) {
  const manifest =
    (await Manifest.tryFind(fetchResult.prefixPath, {
      baseFs: fetchResult.packageFs,
    })) ?? new Manifest();

  const preservedScripts = new Set([`preinstall`, `install`, `postinstall`]);
  for (const scriptName of manifest.scripts.keys())
    if (!preservedScripts.has(scriptName)) manifest.scripts.delete(scriptName);

  return {
    manifest: {
      bin: manifest.bin,
      scripts: manifest.scripts,
      preferUnplugged: manifest.preferUnplugged,
    },
    misc: {
      hasBindingGyp: jsInstallUtils.hasBindingGyp(fetchResult),
      extractHint: jsInstallUtils.getExtractHint(fetchResult),
    },
  };
}

class FuseInstaller implements Installer {
  private readonly asyncActions = new miscUtils.AsyncActions(10);

  private localStore: Map<
    LocatorHash,
    {
      pkg: Package;
      customPackageData: CustomPackageData;
      dependencyMeta: DependencyMeta;
      pnpNode: PackageInformation<NativePath>;
    }
  > = new Map();

  private realLocatorChecksums: Map<LocatorHash, string | null> = new Map();

  constructor(private opts: LinkOptions) {}

  private customData: {
    store: Map<LocatorHash, CustomPackageData>;
  } = {
    store: new Map(),
  };

  attachCustomData(customData: any) {
    this.customData = customData;
  }

  private readonly unpluggedPaths: Set<string> = new Set();

  private async unplugPackageIfNeeded(
    pkg: Package,
    customPackageData: CustomPackageData,
    fetchResult: FetchResult,
    dependencyMeta: DependencyMeta,
    api: InstallPackageExtraApi,
  ) {
    if (this.shouldBeUnplugged(pkg, customPackageData, dependencyMeta)) {
      return this.unplugPackage(pkg, fetchResult, api);
    } else {
      return fetchResult.packageFs;
    }
  }

  private shouldBeUnplugged(
    pkg: Package,
    customPackageData: CustomPackageData,
    dependencyMeta: DependencyMeta,
  ) {
    if (typeof dependencyMeta.unplugged !== `undefined`)
      return dependencyMeta.unplugged;

    if (FORCED_UNPLUG_PACKAGES.has(pkg.identHash)) return true;

    if (pkg.conditions != null) return true;

    if (customPackageData.manifest.preferUnplugged !== null)
      return customPackageData.manifest.preferUnplugged;

    const buildRequest = jsInstallUtils.extractBuildRequest(
      pkg,
      customPackageData,
      dependencyMeta,
      { configuration: this.opts.project.configuration },
    );
    if (buildRequest?.skipped === false || customPackageData.misc.extractHint)
      return true;

    return false;
  }

  private async unplugPackage(
    locator: Locator,
    fetchResult: FetchResult,
    api: InstallPackageExtraApi,
  ) {
    // console.log('unplugPackage', locator.name, locator.reference)
    const unplugPath = getUnpluggedPath(locator, {
      configuration: this.opts.project.configuration,
    });
    if (this.opts.project.disabledLocators.has(locator.locatorHash))
      return new AliasFS(unplugPath, {
        baseFs: fetchResult.packageFs,
        pathUtils: ppath,
      });

    this.unpluggedPaths.add(unplugPath);

    api.holdFetchResult(
      this.asyncActions.set(locator.locatorHash, async () => {
        const readyFile = ppath.join(
          unplugPath,
          fetchResult.prefixPath,
          `.ready`,
        );
        if (await xfs.existsPromise(readyFile)) return;

        // Delete any build state for the locator so it can run anew, this allows users
        // to remove `.yarn/unplugged` and have the builds run again
        this.opts.project.storedBuildState.delete(locator.locatorHash);

        await xfs.mkdirPromise(unplugPath, { recursive: true });
        await xfs.copyPromise(unplugPath, PortablePath.dot, {
          baseFs: fetchResult.packageFs,
          overwrite: false,
        });

        await xfs.writeFilePromise(readyFile, ``);
      }),
    );

    return new CwdFS(unplugPath);
  }

  async installPackage(
    pkg: Package,
    fetchResult: FetchResult,
    api: InstallPackageExtraApi,
  ) {
    let customPackageData = this.customData.store.get(pkg.locatorHash);
    if (typeof customPackageData === `undefined`) {
      customPackageData = await extractCustomPackageData(pkg, fetchResult);
      if (pkg.linkType === LinkType.HARD) {
        this.customData.store.set(pkg.locatorHash, customPackageData);
      }
    }

    // We don't link the package at all if it's for an unsupported platform
    if (
      !structUtils.isPackageCompatible(
        pkg,
        this.opts.project.configuration.getSupportedArchitectures(),
      )
    )
      return { packageLocation: null, buildRequest: null };

    const packageDependencies = new Map<
      string,
      string | [string, string] | null
    >();
    const packagePeers = new Set<string>();

    if (!packageDependencies.has(structUtils.stringifyIdent(pkg)))
      packageDependencies.set(structUtils.stringifyIdent(pkg), pkg.reference);

    let realLocator: Locator = pkg;
    // Only virtual packages should have effective peer dependencies, but the
    // workspaces are a special case because the original packages are kept in
    // the dependency tree even after being virtualized; so in their case we
    // just ignore their declared peer dependencies.
    if (structUtils.isVirtualLocator(pkg)) {
      realLocator = structUtils.devirtualizeLocator(pkg);
      for (const descriptor of pkg.peerDependencies.values()) {
        packageDependencies.set(structUtils.stringifyIdent(descriptor), null);
        packagePeers.add(structUtils.stringifyIdent(descriptor));
      }
    }
    const isVirtual = structUtils.isVirtualLocator(pkg);

    const hasVirtualInstances =
      // Only packages with peer dependencies have virtual instances
      pkg.peerDependencies.size > 0 &&
      // Only packages with peer dependencies have virtual instances
      !isVirtual;

    const mayNeedToBeUnplugged =
      // Virtual instance templates don't need to be unplugged, since they don't truly exist
      !hasVirtualInstances &&
      // We never need to unplug soft links, since we don't control them
      pkg.linkType !== LinkType.SOFT;

    const dependencyMeta = this.opts.project.getDependencyMeta(
      pkg,
      pkg.version,
    );
    let res: FakeFS<PortablePath> = fetchResult.packageFs;

    if (mayNeedToBeUnplugged) {
      res = await this.unplugPackageIfNeeded(
        pkg,
        customPackageData,
        fetchResult,
        dependencyMeta,
        api,
      );
    }

    const packageLocation = ppath.resolve(
      res.getRealPath(),
      fetchResult.prefixPath,
    );

    const pnpNode: PackageInformation<NativePath> = {
      packageLocation: `${npath.fromPortablePath(packageLocation)}/`,
      packageDependencies,
      packagePeers,
      linkType: pkg.linkType,
      discardFromLookup: fetchResult.discardFromLookup ?? false,
    };

    this.localStore.set(pkg.locatorHash, {
      pkg,
      customPackageData,
      dependencyMeta,
      pnpNode,
    });

    // We need ZIP contents checksum for CAS addressing purposes, so we need to strip cache key from checksum here
    const checksum = fetchResult.checksum
      ? fetchResult.checksum.substring(fetchResult.checksum.indexOf(`/`) + 1)
      : null;
    this.realLocatorChecksums.set(realLocator.locatorHash, checksum);

    return {
      packageLocation,
      buildRequest: null,
    };
  }

  async attachInternalDependencies(
    locator: Locator,
    dependencies: Array<[Descriptor, Locator]>,
  ) {
    const slot = this.localStore.get(locator.locatorHash);
    if (typeof slot === `undefined`)
      throw new Error(
        `Assertion failed: Expected information object to have been registered`,
      );

    for (const [descriptor, locator] of dependencies) {
      const target = !structUtils.areIdentsEqual(descriptor, locator)
        ? ([structUtils.stringifyIdent(locator), locator.reference] as [
            string,
            string,
          ])
        : locator.reference;

      slot.pnpNode.packageDependencies.set(
        structUtils.stringifyIdent(descriptor),
        target,
      );
    }
  }

  async attachExternalDependents(
    locator: Locator,
    dependentPaths: Array<PortablePath>,
  ) {
    throw new Error(
      `External dependencies haven't been implemented for the fuse linker`,
    );
  }

  async finalizeInstall() {
    if (this.opts.project.configuration.get(`nodeLinker`) !== `fuse`)
      return undefined;

    const defaultFsLayer = new VirtualFS({
      baseFs: new ZipOpenFS({
        maxOpenFiles: 80,
        readOnlyArchives: true,
      }),
    });

    // let preinstallState = await findInstallState(this.opts.project);
    // const nmModeSetting = this.opts.project.configuration.get(`nmMode`);

    // // Remove build state as well, to force rebuild of all the packages
    // if (preinstallState === null || nmModeSetting !== preinstallState.nmMode) {
    //   this.opts.project.storedBuildState.clear();

    //   preinstallState = {locatorMap: new Map(), binSymlinks: new Map(), locationTree: new Map(), nmMode: nmModeSetting, mtimeMs: 0};
    // }

    const hoistingLimitsByCwd = new Map(
      this.opts.project.workspaces.map((workspace) => {
        let hoistingLimits = this.opts.project.configuration.get(
          `nmHoistingLimits`,
        ) as string;
        try {
          hoistingLimits = miscUtils.validateEnum(
            NodeModulesHoistingLimits,
            workspace.manifest.installConfig?.hoistingLimits ?? hoistingLimits,
          );
        } catch (e) {
          const workspaceName = structUtils.prettyWorkspace(
            this.opts.project.configuration,
            workspace,
          );
          this.opts.report.reportWarning(
            MessageName.INVALID_MANIFEST,
            `${workspaceName}: Invalid 'installConfig.hoistingLimits' value. Expected one of ${Object.values(
              NodeModulesHoistingLimits,
            ).join(`, `)}, using default: "${hoistingLimits}"`,
          );
        }
        return [
          workspace.relativeCwd,
          hoistingLimits as NodeModulesHoistingLimits,
        ];
      }),
    );

    const selfReferencesByCwd = new Map(
      this.opts.project.workspaces.map((workspace) => {
        let selfReferences =
          this.opts.project.configuration.get(`nmSelfReferences`);
        selfReferences =
          workspace.manifest.installConfig?.selfReferences ?? selfReferences;
        return [workspace.relativeCwd, selfReferences as Boolean];
      }),
    );

    const pnpApi: PnpApi = {
      VERSIONS: {
        std: 1,
      },
      topLevel: {
        name: null,
        reference: null,
      },
      getLocator: (name, referencish) => {
        if (Array.isArray(referencish)) {
          return { name: referencish[0], reference: referencish[1] };
        } else {
          return { name, reference: referencish };
        }
      },
      getDependencyTreeRoots: () => {
        return this.opts.project.workspaces.map((workspace) => {
          const anchoredLocator = workspace.anchoredLocator;
          return {
            name: structUtils.stringifyIdent(anchoredLocator),
            reference: anchoredLocator.reference,
          };
        });
      },
      getPackageInformation: (pnpLocator) => {
        const locator =
          pnpLocator.reference === null
            ? this.opts.project.topLevelWorkspace.anchoredLocator
            : structUtils.makeLocator(
                structUtils.parseIdent(pnpLocator.name),
                pnpLocator.reference,
              );

        const slot = this.localStore.get(locator.locatorHash);
        if (typeof slot === `undefined`)
          throw new Error(
            `Assertion failed: Expected the package reference to have been registered`,
          );

        return slot.pnpNode;
      },
      findPackageLocator: (location) => {
        const workspace = this.opts.project.tryWorkspaceByCwd(
          npath.toPortablePath(location),
        );
        if (workspace !== null) {
          const anchoredLocator = workspace.anchoredLocator;
          return {
            name: structUtils.stringifyIdent(anchoredLocator),
            reference: anchoredLocator.reference,
          };
        }

        throw new Error(`Assertion failed: Unimplemented`);
      },
      resolveToUnqualified: () => {
        throw new Error(`Assertion failed: Unimplemented`);
      },
      resolveUnqualified: () => {
        throw new Error(`Assertion failed: Unimplemented`);
      },
      resolveRequest: () => {
        throw new Error(`Assertion failed: Unimplemented`);
      },
      resolveVirtual: (path) => {
        return npath.fromPortablePath(
          VirtualFS.resolveVirtual(npath.toPortablePath(path)),
        );
      },
    };

    const { tree, errors, preserveSymlinksRequired } = buildNodeModulesTree(
      pnpApi,
      {
        pnpifyFs: false,
        validateExternalSoftLinks: true,
        hoistingLimitsByCwd,
        project: this.opts.project,
        selfReferencesByCwd,
      },
    );
    if (!tree) {
      for (const { messageName, text } of errors)
        this.opts.report.reportError(messageName, text);

      return undefined;
    }
    const locatorMap = buildLocatorMap(tree);

    // await persistNodeModules(preinstallState, locatorMap, {
    //   baseFs: defaultFsLayer,
    //   project: this.opts.project,
    //   report: this.opts.report,
    //   realLocatorChecksums: this.realLocatorChecksums,
    //   loadManifest: async locatorKey => {
    //     const locator = structUtils.parseLocator(locatorKey);

    //     const slot = this.localStore.get(locator.locatorHash);
    //     if (typeof slot === `undefined`)
    //       throw new Error(`Assertion failed: Expected the slot to exist`);

    //     return slot.customPackageData.manifest;
    //   },
    // });

    const installStatuses: Array<FinalizeInstallStatus> = [];

    for (const [locatorKey, installRecord] of locatorMap.entries()) {
      if (isLinkLocator(locatorKey)) continue;

      const locator = structUtils.parseLocator(locatorKey);
      const slot = this.localStore.get(locator.locatorHash);
      if (typeof slot === `undefined`)
        throw new Error(`Assertion failed: Expected the slot to exist`);

      // Workspaces are built by the core
      if (this.opts.project.tryWorkspaceByLocator(slot.pkg)) continue;

      const buildRequest = jsInstallUtils.extractBuildRequest(
        slot.pkg,
        slot.customPackageData,
        slot.dependencyMeta,
        { configuration: this.opts.project.configuration },
      );
      if (!buildRequest) continue;

      installStatuses.push({
        buildLocations: installRecord.locations,
        locator,
        buildRequest,
      });
    }

    if (preserveSymlinksRequired)
      this.opts.report.reportWarning(
        MessageName.NM_PRESERVE_SYMLINKS_REQUIRED,
        `The application uses portals and that's why ${formatUtils.pretty(
          this.opts.project.configuration,
          `--preserve-symlinks`,
          formatUtils.Type.CODE,
        )} Node option is required for launching it`,
      );

    const locationTree = buildLocationTree(locatorMap, {
      skipPrefix: this.opts.project.cwd,
    });

    const binSymlinks = await createBinSymlinkMap(
      locatorMap,
      locationTree,
      this.opts.project.cwd,
      {
        loadManifest: async (locatorKey) => {
          const locator = structUtils.parseLocator(locatorKey);

          const slot = this.localStore.get(locator.locatorHash);
          if (typeof slot === `undefined`)
            throw new Error(`Assertion failed: Expected the slot to exist`);

          return slot.customPackageData.manifest;
        },
      },
    );

    const installStatePath = ppath.join(
      this.opts.project.cwd,
      `.yarn/fuse-state.json`,
    );
    // console.log(locatorMap);

    // console.log(inspect(locationTree, { depth: 10 }));
    const fuseState = buildFuseTree(locationTree, binSymlinks);
    // console.log(tree)
    await xfs.changeFilePromise(
      installStatePath,
      JSON.stringify(fuseState, null, 2),
      {},
    );

    const installFuseTree: FuseData = { roots: {} };

    for (const status of installStatuses) {
      break;
      const started = Date.now();
      if (status.buildRequest.skipped) continue;
      const unpluggedPath = getUnpluggedPath(status.locator, {
        configuration: this.opts.project.configuration,
      });
      status.buildLocations = [unpluggedPath];
      const rootSlot = this.localStore.get(status.locator.locatorHash);
      console.log(unpluggedPath);
      const rootInfo: PackageInformation<NativePath> = {
        ...rootSlot.pnpNode,
        packageLocation: `${npath.fromPortablePath(unpluggedPath)}/`,
        // linkType: LinkType.SOFT,
      };

      const otherPnpApi: PnpApi = {
        ...pnpApi,
        getDependencyTreeRoots: () => [],
        findPackageLocator: (location) => {
          if (location === rootInfo.packageLocation) {
            return {
              name: 'root',
              reference: 'root',
            };
          }
          throw new Error('not impl');
        },
        resolveVirtual: (path) => {
          return npath.fromPortablePath(
            VirtualFS.resolveVirtual(npath.toPortablePath(path)),
          );
        },
        getPackageInformation: (pnpLocator) => {
          if (pnpLocator.reference === null) {
            return rootInfo;
          }
          if (pnpLocator.reference == 'root' && pnpLocator.name == 'root') {
            return rootInfo;
          }
          const locator = structUtils.makeLocator(
            structUtils.parseIdent(pnpLocator.name),
            pnpLocator.reference,
          );

          const slot = this.localStore.get(locator.locatorHash);
          if (typeof slot === `undefined`)
            throw new Error(
              `Assertion failed: Expected the package reference to have been registered`,
            );

          return slot.pnpNode;
        },
      };

      const { tree, errors, preserveSymlinksRequired } = buildNodeModulesTree(
        otherPnpApi,
        {
          pnpifyFs: false,
          validateExternalSoftLinks: true,
          hoistingLimitsByCwd,
          project: this.opts.project,
          selfReferencesByCwd,
        },
      );
      if (!tree) {
        for (const { messageName, text } of errors)
          this.opts.report.reportError(messageName, text);

        return undefined;
      }
      const locatorMap = buildLocatorMap(tree);
      const locationTree = buildLocationTree(locatorMap, {
        skipPrefix: unpluggedPath,
      });
      // const binSymlinks = await createBinSymlinkMap(
      //   locatorMap,
      //   locationTree,
      //   unpluggedPath,
      //   {
      //     loadManifest: async (locatorKey) => {
      //       const locator = structUtils.parseLocator(locatorKey);
      //       if (locator.reference === 'root' && locator.name === 'root') {
      //         return rootSlot.customPackageData.manifest
      //       }
      //       const slot = this.localStore.get(locator.locatorHash);
      //       if (typeof slot === `undefined`)
      //         throw new Error(`Assertion failed: Expected the slot to exist`);

      //       return slot.customPackageData.manifest;
      //     },
      //   },
      // );
      const fuseTree = buildFuseTree(locationTree, new Map());
      Object.assign(installFuseTree.roots, fuseTree.roots);
      console.log(Date.now() - started);
    }

    await this.asyncActions.wait();
    const pnpUnpluggedFolder = this.opts.project.configuration.get(`pnpUnpluggedFolder2`);
    if (this.unpluggedPaths.size === 0) {
      await xfs.removePromise(pnpUnpluggedFolder);
    } else {
      for (const entry of await xfs.readdirPromise(pnpUnpluggedFolder)) {
        const unpluggedPath = ppath.resolve(pnpUnpluggedFolder, entry);
        if (!this.unpluggedPaths.has(unpluggedPath)) {
          await xfs.removePromise(unpluggedPath);
        }
      }
    }

    await runFuse(installStatePath)

    return {
      customData: this.customData,
      records: installStatuses,
    };
  }
}
class FuseLinker implements Linker {
  supportsPackage(pkg: Package, opts: MinimalLinkOptions): boolean {
    return this.isEnabled(opts);
  }
  async findPackageLocation(locator: Locator, opts: LinkOptions) {
    // console.error(locator);
  }

  async findPackageLocator(location: PortablePath, opts: LinkOptions) {
    // console.error(locator);
  }
  getCustomDataKey(): string {
    return JSON.stringify({
      name: `Fuse`,
      version: 1,
    });
  }

  private isEnabled(opts: MinimalLinkOptions) {
    return opts.project.configuration.get(`nodeLinker`) === `fuse`;
  }
  makeInstaller(opts: LinkOptions): Installer {
    return new FuseInstaller(opts);
  }
}

declare module '@yarnpkg/core' {
  interface ConfigurationValueMap {
    pnpUnpluggedFolder2: PortablePath;
  }
}

const plugin: Plugin<Hooks> = {
  linkers: [FuseLinker],
  commands: [],
  configuration: {
    pnpUnpluggedFolder2: {
      description: `Folder where the unplugged packages must be stored`,
      type: SettingsType.ABSOLUTE_PATH,
      default: `./.yarn/unplugged2`,
    },
  },
  hooks: {
    async populateYarnPaths(project, define) {
      define(project.configuration.get(`pnpUnpluggedFolder`));
    },
    afterAllInstalled(project, options) {},
  },
};

export default plugin;
