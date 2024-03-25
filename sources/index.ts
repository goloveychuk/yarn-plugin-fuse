import {Hooks, Plugin, SettingsType}        from '@yarnpkg/core';
import {xfs}                                from '@yarnpkg/fslib';
import {NodeModulesHoistingLimits}          from '@yarnpkg/nm';

import {NodeModulesLinker, NodeModulesMode} from './NodeModulesLinker';
import {getGlobalHardlinksStore}            from './NodeModulesLinker';



const plugin: Plugin<Hooks> = {
  // hooks: {
  //   cleanGlobalArtifacts: async configuration => {
  //     const globalHardlinksDirectory = getGlobalHardlinksStore(configuration);
  //     await xfs.removePromise(globalHardlinksDirectory);
  //   },
  // },
  configuration: {

  },
  linkers: [
    NodeModulesLinker,
  ],
};

// eslint-disable-next-line arca/no-default-export
export default plugin;
