import { structUtils, Report, Manifest, miscUtils, formatUtils } from '@yarnpkg/core';
import { Locator, Package, FinalizeInstallStatus, hashUtils } from '@yarnpkg/core';
import { Linker, LinkOptions, MinimalLinkOptions, LinkType, WindowsLinkType } from '@yarnpkg/core';
import { LocatorHash, Descriptor, DependencyMeta, Configuration } from '@yarnpkg/core';
import { MessageName, Project, FetchResult, Installer, httpUtils } from '@yarnpkg/core';
import { PortablePath, npath, ppath, Filename } from '@yarnpkg/fslib';
import { VirtualFS, xfs, FakeFS, NativePath } from '@yarnpkg/fslib';
import { buildNodeModulesTree } from '@yarnpkg/nm';
import { NodeModulesLocatorMap, buildLocatorMap, NodeModulesHoistingLimits } from '@yarnpkg/nm';
import { parseSyml } from '@yarnpkg/parsers';
import { jsInstallUtils } from '@yarnpkg/plugin-pnp';
import { PnpApi, PackageInformation } from '@yarnpkg/pnp';
import { UsageError } from 'clipanion';
import { runFuse } from './runFuse';
import { buildFuseTree } from './utils';

const yarn3 = !jsInstallUtils.extractBuildRequest

const STATE_FILE_VERSION = 1;
const NODE_MODULES = `node_modules` as Filename;
const DOT_BIN = `.bin` as Filename;
const INSTALL_STATE_FILE = `.yarn-state.yml` as Filename;
const MTIME_ACCURANCY = 1000;

type InstallState = { locatorMap: NodeModulesLocatorMap, locationTree: LocationTree, binSymlinks: BinSymlinkMap, nmMode: NodeModulesMode, mtimeMs: number };
export type BinSymlinkMap = Map<PortablePath, Map<Filename, PortablePath>>;
type LoadManifest = (locator: LocatorKey, installLocation: PortablePath) => Promise<Pick<Manifest, 'bin'>>;

export enum NodeModulesMode {
  CLASSIC = `classic`,
  HARDLINKS_LOCAL = `hardlinks-local`,
  HARDLINKS_GLOBAL = `hardlinks-global`,
}

export class NodeModulesLinker implements Linker {
  private installStateCache: Map<string, Promise<InstallState | null>> = new Map();

  getCustomDataKey() {
    return JSON.stringify({
      name: `FuseLinker`,
      version: 3,
    });
  }

  supportsPackage(pkg: Package, opts: MinimalLinkOptions) {
    return this.isEnabled(opts);
  }

  async findPackageLocation(locator: Locator, opts: LinkOptions) {
    if (!this.isEnabled(opts))
      throw new Error(`Assertion failed: Expected the fuse linker to be enabled`);

    const workspace = opts.project.tryWorkspaceByLocator(locator);
    if (workspace)
      return workspace.cwd;

    const installState = await miscUtils.getFactoryWithDefault(this.installStateCache, opts.project.cwd, async () => {
      return await findInstallState(opts.project, { unrollAliases: true });
    });

    if (installState === null)
      throw new UsageError(`Couldn't find the node_modules state file - running an install might help (findPackageLocation)`);

    const locatorInfo = installState.locatorMap.get(structUtils.stringifyLocator(locator));
    if (!locatorInfo) {
      const err = new UsageError(`Couldn't find ${structUtils.prettyLocator(opts.project.configuration, locator)} in the currently installed node_modules map - running an install might help`);
      (err as any).code = `LOCATOR_NOT_INSTALLED`;
      throw err;
    }

    // Sort locations from shallowest to deepest in terms of directory nesting
    const sortedLocations = locatorInfo.locations.sort((loc1, loc2) => loc1.split(ppath.sep).length - loc2.split(ppath.sep).length);
    // Find the location with shallowest directory nesting that starts inside node_modules of cwd
    const startingCwdModules = ppath.join(opts.project.configuration.startingCwd, NODE_MODULES);
    return sortedLocations.find(location => ppath.contains(startingCwdModules, location)) || locatorInfo.locations[0];
  }

  async findPackageLocator(location: PortablePath, opts: LinkOptions) {
    if (!this.isEnabled(opts))
      return null;

    const installState = await miscUtils.getFactoryWithDefault(this.installStateCache, opts.project.cwd, async () => {
      return await findInstallState(opts.project, { unrollAliases: true });
    });

    if (installState === null)
      return null;

    const { locationRoot, segments } = parseLocation(ppath.resolve(location), { skipPrefix: opts.project.cwd });

    let locationNode = installState.locationTree.get(locationRoot);
    if (!locationNode)
      return null;

    let locator = locationNode.locator!;
    for (const segment of segments) {
      locationNode = locationNode.children.get(segment);
      if (!locationNode)
        break;
      locator = locationNode.locator || locator;
    }

    return structUtils.parseLocator(locator);
  }

  makeInstaller(opts: LinkOptions) {
    return new NodeModulesInstaller(opts);
  }

  private isEnabled(opts: MinimalLinkOptions) {
    return opts.project.configuration.get(`nodeLinker`) === `fuse`;
  }
}

class NodeModulesInstaller implements Installer {
  // Stores data that we need to extract in the `installPackage` step but use
  // in the `finalizeInstall` step. Contrary to custom data this isn't persisted
  // anywhere - we literally just use it for the lifetime of the installer then
  // discard it.
  private localStore: Map<LocatorHash, {
    pkg: Package;
    customPackageData: CustomPackageData;
    dependencyMeta: DependencyMeta;
    pnpNode: PackageInformation<NativePath>;
  }> = new Map();

  getCustomDataKey() { //needed for yarn 3
    return JSON.stringify({
      name: `FuseLinker`,
      version: 3,
    });
  }

  private realLocatorChecksums: Map<LocatorHash, string | null> = new Map();

  constructor(private opts: LinkOptions) {
    // Nothing to do
  }

  private customData: {
    store: Map<LocatorHash, CustomPackageData>;
  } = {
      store: new Map(),
    };

  attachCustomData(customData: any) {
    this.customData = customData;
  }

  async installPackage(pkg: Package, fetchResult: FetchResult) {
    const packageLocation = ppath.resolve(fetchResult.packageFs.getRealPath(), fetchResult.prefixPath);

    let customPackageData = this.customData.store.get(pkg.locatorHash);
    if (typeof customPackageData === `undefined`) {
      customPackageData = await extractCustomPackageData(pkg, fetchResult);
      if (pkg.linkType === LinkType.HARD) {
        this.customData.store.set(pkg.locatorHash, customPackageData);
      }
    }

    // We don't link the package at all if it's for an unsupported platform
    if (!structUtils.isPackageCompatible(pkg, this.opts.project.configuration.getSupportedArchitectures()))
      return {
        packageLocation: null, buildRequest: null,
        buildDirective: null //yarn3
      };

    const packageDependencies = new Map<string, string | [string, string] | null>();
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
      dependencyMeta: this.opts.project.getDependencyMeta(pkg, pkg.version),
      pnpNode,
    });

    // We need ZIP contents checksum for CAS addressing purposes, so we need to strip cache key from checksum here
    const checksum = fetchResult.checksum ? fetchResult.checksum.substring(fetchResult.checksum.indexOf(`/`) + 1) : null;
    this.realLocatorChecksums.set(realLocator.locatorHash, checksum);

    return {
      packageLocation,
      buildRequest: null,
      buildDirective: null, //yarn3
    };
  }

  async attachInternalDependencies(locator: Locator, dependencies: Array<[Descriptor, Locator]>) {
    const slot = this.localStore.get(locator.locatorHash);
    if (typeof slot === `undefined`)
      throw new Error(`Assertion failed: Expected information object to have been registered`);

    for (const [descriptor, locator] of dependencies) {
      const target = !structUtils.areIdentsEqual(descriptor, locator)
        ? [structUtils.stringifyIdent(locator), locator.reference] as [string, string]
        : locator.reference;

      slot.pnpNode.packageDependencies.set(structUtils.stringifyIdent(descriptor), target);
    }
  }

  async attachExternalDependents(locator: Locator, dependentPaths: Array<PortablePath>) {
    throw new Error(`External dependencies haven't been implemented for the fuse linker`);
  }

  async finalizeInstall() {
    if (this.opts.project.configuration.get(`nodeLinker`) !== `fuse`)
      return undefined;



    let preinstallState = await findInstallState(this.opts.project);
    const nmModeSetting = this.opts.project.configuration.get(`nmMode`);

    // Remove build state as well, to force rebuild of all the packages
    if (preinstallState === null || nmModeSetting !== preinstallState.nmMode) {
      this.opts.project.storedBuildState.clear();

      preinstallState = { locatorMap: new Map(), binSymlinks: new Map(), locationTree: new Map(), nmMode: nmModeSetting as any, mtimeMs: 0 };
    }

    const hoistingLimitsByCwd = new Map(this.opts.project.workspaces.map(workspace => {
      let hoistingLimits = this.opts.project.configuration.get(`nmHoistingLimits`);
      try {
        hoistingLimits = miscUtils.validateEnum(NodeModulesHoistingLimits, workspace.manifest.installConfig?.hoistingLimits ?? hoistingLimits as any);
      } catch (e) {
        const workspaceName = structUtils.prettyWorkspace(this.opts.project.configuration, workspace);
        this.opts.report.reportWarning(MessageName.INVALID_MANIFEST, `${workspaceName}: Invalid 'installConfig.hoistingLimits' value. Expected one of ${Object.values(NodeModulesHoistingLimits).join(`, `)}, using default: "${hoistingLimits}"`);
      }
      return [workspace.relativeCwd, hoistingLimits as NodeModulesHoistingLimits];
    }));

    const selfReferencesByCwd = new Map(this.opts.project.workspaces.map(workspace => {
      let selfReferences = this.opts.project.configuration.get(`nmSelfReferences`);
      selfReferences = workspace.manifest.installConfig?.selfReferences ?? selfReferences;
      return [workspace.relativeCwd, selfReferences as boolean];
    }));

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
        return this.opts.project.workspaces.map(workspace => {
          const anchoredLocator = workspace.anchoredLocator;
          return { name: structUtils.stringifyIdent(yarn3 ? (workspace as any).locator : anchoredLocator), reference: anchoredLocator.reference };
        });
      },
      getPackageInformation: pnpLocator => {
        const locator = pnpLocator.reference === null
          ? this.opts.project.topLevelWorkspace.anchoredLocator
          : structUtils.makeLocator(structUtils.parseIdent(pnpLocator.name), pnpLocator.reference);

        const slot = this.localStore.get(locator.locatorHash);
        if (typeof slot === `undefined`)
          throw new Error(`Assertion failed: Expected the package reference to have been registered`);

        return slot.pnpNode;
      },
      findPackageLocator: location => {
        const workspace = this.opts.project.tryWorkspaceByCwd(npath.toPortablePath(location));
        if (workspace !== null) {
          const anchoredLocator = workspace.anchoredLocator;
          return { name: structUtils.stringifyIdent(anchoredLocator), reference: anchoredLocator.reference };
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
      resolveVirtual: path => {
        return npath.fromPortablePath(VirtualFS.resolveVirtual(npath.toPortablePath(path)));
      },
    };

    const { tree, errors, preserveSymlinksRequired } = buildNodeModulesTree(pnpApi, { pnpifyFs: false, validateExternalSoftLinks: true, hoistingLimitsByCwd, project: this.opts.project, selfReferencesByCwd });
    if (!tree) {
      for (const { messageName, text } of errors)
        this.opts.report.reportError(messageName, text);

      return undefined;
    }
    const locatorMap = buildLocatorMap(tree);


    await persistNodeModules(preinstallState, locatorMap, {
      project: this.opts.project,
      report: this.opts.report,
      realLocatorChecksums: this.realLocatorChecksums,
      loadManifest: async locatorKey => {
        const locator = structUtils.parseLocator(locatorKey);

        const slot = this.localStore.get(locator.locatorHash);
        if (typeof slot === `undefined`)
          throw new Error(`Assertion failed: Expected the slot to exist`);

        return slot.customPackageData.manifest;
      },
    });

    const installStatuses: Array<FinalizeInstallStatus> = [];

    for (const [locatorKey, installRecord] of locatorMap.entries()) {
      if (isLinkLocator(locatorKey))
        continue;

      const locator = structUtils.parseLocator(locatorKey);
      const slot = this.localStore.get(locator.locatorHash);
      if (typeof slot === `undefined`)
        throw new Error(`Assertion failed: Expected the slot to exist`);

      // Workspaces are built by the core
      if (this.opts.project.tryWorkspaceByLocator(slot.pkg))
        continue;
      
      if (yarn3) {
        const buildScripts = (jsInstallUtils as any).extractBuildScripts(slot.pkg, slot.customPackageData, slot.dependencyMeta, { configuration: this.opts.project.configuration, report: this.opts.report });
        if (buildScripts.length === 0)
          continue;

        installStatuses.push({
          buildLocations: installRecord.locations,
          //@ts-expect-error
          locatorHash: locator.locatorHash,
          buildDirective: buildScripts,
        });
      } else {
        const buildRequest = jsInstallUtils.extractBuildRequest(slot.pkg, slot.customPackageData, slot.dependencyMeta, { configuration: this.opts.project.configuration });
        if (!buildRequest)
          continue;

        installStatuses.push({
          buildLocations: installRecord.locations,
          locator,
          buildRequest,
        });
      }
    }

    if (preserveSymlinksRequired)
      this.opts.report.reportWarning(MessageName.NM_PRESERVE_SYMLINKS_REQUIRED, `The application uses portals and that's why ${formatUtils.pretty(this.opts.project.configuration, `--preserve-symlinks`, formatUtils.Type.CODE)} Node option is required for launching it`);

    return {
      customData: this.customData,
      records: installStatuses,
    };
  }
}


type UnboxPromise<T extends Promise<any>> = T extends Promise<infer U> ? U : never;
type CustomPackageData = UnboxPromise<ReturnType<typeof extractCustomPackageData>>;

async function extractCustomPackageData(pkg: Package, fetchResult: FetchResult) {
  const manifest = await Manifest.tryFind(fetchResult.prefixPath, { baseFs: fetchResult.packageFs }) ?? new Manifest();

  const preservedScripts = new Set([`preinstall`, `install`, `postinstall`]);
  for (const scriptName of manifest.scripts.keys())
    if (!preservedScripts.has(scriptName))
      manifest.scripts.delete(scriptName);

  return {
    manifest: {
      bin: manifest.bin,
      scripts: manifest.scripts,
    },
    misc: {
      extractHint: yarn3 ? jsInstallUtils.getExtractHint(fetchResult): undefined,
      hasBindingGyp: jsInstallUtils.hasBindingGyp(fetchResult),
    },
  };
}

async function writeInstallState(project: Project, locatorMap: NodeModulesLocatorMap, binSymlinks: BinSymlinkMap, nmMode: { value: NodeModulesMode }, { installChangedByUser }: { installChangedByUser: boolean }) {
  let locatorState = ``;

  locatorState += `# Warning: This file is automatically generated. Removing it is fine, but will\n`;
  locatorState += `# cause your node_modules installation to become invalidated.\n`;
  locatorState += `\n`;
  locatorState += `__metadata:\n`;
  locatorState += `  version: ${STATE_FILE_VERSION}\n`;
  locatorState += `  nmMode: ${nmMode.value}\n`;

  const locators = Array.from(locatorMap.keys()).sort();
  const topLevelLocator = structUtils.stringifyLocator(project.topLevelWorkspace.anchoredLocator);

  for (const locator of locators) {
    const installRecord = locatorMap.get(locator)!;
    locatorState += `\n`;
    locatorState += `${JSON.stringify(locator)}:\n`;
    locatorState += `  locations:\n`;

    for (const location of installRecord.locations) {
      const internalPath = ppath.contains(project.cwd, location);
      if (internalPath === null)
        throw new Error(`Assertion failed: Expected the path to be within the project (${location})`);

      locatorState += `    - ${JSON.stringify(internalPath)}\n`;
    }

    if (installRecord.aliases.length > 0) {
      locatorState += `  aliases:\n`;
      for (const alias of installRecord.aliases) {
        locatorState += `    - ${JSON.stringify(alias)}\n`;
      }
    }

    if (locator === topLevelLocator && binSymlinks.size > 0) {
      locatorState += `  bin:\n`;
      for (const [location, symlinks] of binSymlinks) {
        const internalPath = ppath.contains(project.cwd, location);
        if (internalPath === null)
          throw new Error(`Assertion failed: Expected the path to be within the project (${location})`);

        locatorState += `    ${JSON.stringify(internalPath)}:\n`;
        for (const [name, target] of symlinks) {
          const relativePath = ppath.relative(ppath.join(location, NODE_MODULES), target);
          locatorState += `      ${JSON.stringify(name)}: ${JSON.stringify(relativePath)}\n`;
        }
      }
    }
  }

  const rootPath = project.cwd;
  const installStatePath = ppath.join(rootPath, NODE_MODULES, INSTALL_STATE_FILE);

  // Force install state file rewrite, so that it has mtime bigger than all node_modules subfolders
  if (installChangedByUser)
    await xfs.removePromise(installStatePath);

  await xfs.changeFilePromise(installStatePath, locatorState, {
    automaticNewlines: true,
  });
}

async function findInstallState(project: Project, { unrollAliases = false }: { unrollAliases?: boolean } = {}): Promise<InstallState | null> {
  const rootPath = project.cwd;
  const installStatePath = ppath.join(rootPath, NODE_MODULES, INSTALL_STATE_FILE);

  let stats;
  try {
    stats = await xfs.statPromise(installStatePath);
  } catch (e) {
  }

  if (!stats)
    return null;

  const locatorState = parseSyml(await xfs.readFilePromise(installStatePath, `utf8`));

  // If we have a higher serialized version than we can handle, ignore the state alltogether
  if (locatorState.__metadata.version > STATE_FILE_VERSION)
    return null;

  const nmMode = locatorState.__metadata.nmMode || NodeModulesMode.CLASSIC;

  const locatorMap: NodeModulesLocatorMap = new Map();
  const binSymlinks: BinSymlinkMap = new Map();

  delete locatorState.__metadata;

  for (const [locatorStr, installRecord] of Object.entries(locatorState)) {
    const locations = installRecord.locations.map((location: PortablePath) => {
      return ppath.join(rootPath, location);
    });

    const recordSymlinks = installRecord.bin;
    if (recordSymlinks) {
      for (const [relativeLocation, locationSymlinks] of Object.entries(recordSymlinks)) {
        const location = ppath.join(rootPath, npath.toPortablePath(relativeLocation));
        const symlinks = miscUtils.getMapWithDefault(binSymlinks, location);
        for (const [name, target] of Object.entries(locationSymlinks as any)) {
          symlinks.set(name as Filename, npath.toPortablePath([location, NODE_MODULES, target].join(ppath.sep)));
        }
      }
    }

    locatorMap.set(locatorStr, {
      target: PortablePath.dot,
      linkType: LinkType.HARD,
      locations,
      aliases: installRecord.aliases || [],
    });

    if (unrollAliases && installRecord.aliases) {
      for (const reference of installRecord.aliases) {
        const { scope, name } = structUtils.parseLocator(locatorStr);

        const alias = structUtils.makeLocator(structUtils.makeIdent(scope, name), reference);
        const aliasStr = structUtils.stringifyLocator(alias);

        locatorMap.set(aliasStr, {
          target: PortablePath.dot,
          linkType: LinkType.HARD,
          locations,
          aliases: [],
        });
      }
    }
  }

  return { locatorMap, binSymlinks, locationTree: buildLocationTree(locatorMap, { skipPrefix: project.cwd }), nmMode, mtimeMs: stats.mtimeMs };
}

const removeDir = async (dir: PortablePath, options: { contentsOnly: boolean, innerLoop?: boolean, allowSymlink?: boolean }): Promise<any> => {
  if (dir.split(ppath.sep).indexOf(NODE_MODULES) < 0)
    throw new Error(`Assertion failed: trying to remove dir that doesn't contain node_modules: ${dir}`);

  try {
    if (!options.innerLoop) {
      const stats = options.allowSymlink ? await xfs.statPromise(dir) : await xfs.lstatPromise(dir);
      if (options.allowSymlink && !stats.isDirectory() ||
        (!options.allowSymlink && stats.isSymbolicLink())) {
        await xfs.unlinkPromise(dir);
        return;
      }
    }
    const entries = await xfs.readdirPromise(dir, { withFileTypes: true });
    for (const entry of entries) {
      const targetPath = ppath.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== NODE_MODULES || (options && options.innerLoop)) {
          await removeDir(targetPath, { innerLoop: true, contentsOnly: false });
        }
      } else {
        await xfs.unlinkPromise(targetPath);
      }
    }
    if (!options.contentsOnly) {
      await xfs.rmdirPromise(dir);
    }
  } catch (e: any) {
    if (e.code !== `ENOENT` && e.code !== `ENOTEMPTY`) {
      throw e;
    }
  }
};


type LocatorKey = string;
export type LocationNode = { children: Map<Filename, LocationNode>, locator?: LocatorKey, linkType: LinkType, target?: string };
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
export type LocationTree = Map<LocationRoot, LocationNode>;

const parseLocation = (location: PortablePath, { skipPrefix }: { skipPrefix: PortablePath }): { locationRoot: PortablePath, segments: Array<Filename> } => {
  const projectRelativePath = ppath.contains(skipPrefix, location);
  if (projectRelativePath === null)
    throw new Error(`Assertion failed: Writing attempt prevented to ${location} which is outside project root: ${skipPrefix}`);

  const allSegments = projectRelativePath
    .split(ppath.sep)
    // Ignore empty segments (after trailing slashes)
    .filter(segment => segment !== ``);
  const nmIndex = allSegments.indexOf(NODE_MODULES);

  // Project path, up until the first node_modules segment
  const relativeRoot = allSegments.slice(0, nmIndex).join(ppath.sep) as PortablePath;
  const locationRoot = ppath.join(skipPrefix, relativeRoot);

  // All segments that follow
  const segments = allSegments.slice(nmIndex) as Array<Filename>;

  return { locationRoot, segments };
};

const buildLocationTree = (locatorMap: NodeModulesLocatorMap | null, { skipPrefix }: { skipPrefix: PortablePath }): LocationTree => {
  const locationTree: LocationTree = new Map();
  if (locatorMap === null)
    return locationTree;

  const makeNode: () => LocationNode = () => ({
    children: new Map(),
    linkType: LinkType.HARD,
  });

  for (const [locator, info] of locatorMap.entries()) {
    if (info.linkType === LinkType.SOFT) {
      const internalPath = ppath.contains(skipPrefix, info.target);
      if (internalPath !== null) {
        const node = miscUtils.getFactoryWithDefault(locationTree, info.target, makeNode);
        node.locator = locator;
        node.linkType = info.linkType;
      }
    }

    for (const location of info.locations) {
      const { locationRoot, segments } = parseLocation(location, { skipPrefix });

      let node = miscUtils.getFactoryWithDefault(locationTree, locationRoot, makeNode);

      for (let idx = 0; idx < segments.length; ++idx) {
        const segment = segments[idx];
        // '.' segment exists only for top-level locator, skip it
        if (segment !== `.`) {
          const nextNode = miscUtils.getFactoryWithDefault(node.children, segment, makeNode);

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


enum DirEntryKind {
  FILE = `file`, DIRECTORY = `directory`, SYMLINK = `symlink`,
}

type DirEntry = {
  kind: DirEntryKind.FILE;
  mode: number;
  digest?: string;
  mtimeMs?: number;
} | {
  kind: DirEntryKind.DIRECTORY;
} | {
  kind: DirEntryKind.SYMLINK;
  symlinkTo: PortablePath;
};



function isLinkLocator(locatorKey: LocatorKey): boolean {
  let descriptor = structUtils.parseDescriptor(locatorKey);
  if (structUtils.isVirtualDescriptor(descriptor))
    descriptor = structUtils.devirtualizeDescriptor(descriptor);

  return descriptor.range.startsWith(`link:`);
}

async function createBinSymlinkMap(installState: NodeModulesLocatorMap, locationTree: LocationTree, projectRoot: PortablePath, { loadManifest }: { loadManifest: LoadManifest }) {
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
          bin.set(name, value);
        }
      }
    }

    locatorScriptMap.set(locatorKey, bin);
  }

  const binSymlinks: BinSymlinkMap = new Map();

  const getBinSymlinks = (location: PortablePath, parentLocatorLocation: PortablePath, node: LocationNode): Map<Filename, PortablePath> => {
    const symlinks = new Map();
    const internalPath = ppath.contains(projectRoot, location);
    if (node.locator && internalPath !== null) {
      const binScripts = locatorScriptMap.get(node.locator)!;
      for (const [filename, scriptPath] of binScripts) {
        const symlinkTarget = ppath.join(location, npath.toPortablePath(scriptPath));
        symlinks.set(filename, symlinkTarget);
      }
      for (const [childLocation, childNode] of node.children) {
        const absChildLocation = ppath.join(location, childLocation);
        const childSymlinks = getBinSymlinks(absChildLocation, absChildLocation, childNode);
        if (childSymlinks.size > 0) {
          binSymlinks.set(location, new Map([...(binSymlinks.get(location) || new Map()), ...childSymlinks]));
        }
      }
    } else {
      for (const [childLocation, childNode] of node.children) {
        const childSymlinks = getBinSymlinks(ppath.join(location, childLocation), parentLocatorLocation, childNode);
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
      binSymlinks.set(location, new Map([...(binSymlinks.get(location) || new Map()), ...symlinks]));
    }
  }

  return binSymlinks;
}


async function persistNodeModules(preinstallState: InstallState, installState: NodeModulesLocatorMap, { project, report, loadManifest, realLocatorChecksums }: { project: Project, report: Report, loadManifest: LoadManifest, realLocatorChecksums: Map<LocatorHash, string | null> }) {
  const rootNmDirPath = ppath.join(project.cwd, NODE_MODULES);

  const locationTree = buildLocationTree(installState, { skipPrefix: project.cwd });


  const binSymlinks = await createBinSymlinkMap(installState, locationTree, project.cwd, { loadManifest });

  const nmModeSetting = project.configuration.get(`nmMode`) as any;
  const nmMode = { value: nmModeSetting };

  const fuseStatePath = ppath.join(
    project.cwd,
    `.yarn/fuse-state.json`,
  );

  const fuseState = buildFuseTree(locationTree, binSymlinks);

  await xfs.changeFilePromise(fuseStatePath, JSON.stringify(fuseState), {});

  const fetcher = async (url: string) => {
    const resp = await httpUtils.request(url, null, {configuration: project.configuration, })
    if (resp.statusCode !== 200) {
      throw new Error(`Failed to download ${url}, status code: ${resp.statusCode}`);
    }
    return resp.body;
  }
  
  const nmPath = ppath.join(project.cwd, NODE_MODULES);
  await runFuse(fetcher, nmPath, fuseStatePath);

  let installChangedByUser = false // ??
  await writeInstallState(project, installState, binSymlinks, nmMode, { installChangedByUser });
}


export function getGlobalHardlinksStore(configuration: Configuration): PortablePath {
  return ppath.join(configuration.get(`globalFolder`), `store` as Filename);
}


