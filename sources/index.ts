import {Hooks, Plugin, SettingsType}        from '@yarnpkg/core';
import {xfs}                                from '@yarnpkg/fslib';
import {NodeModulesHoistingLimits}          from '@yarnpkg/nm';

import {NodeModulesLinker, NodeModulesMode} from './NodeModulesLinker';




const plugin: Plugin<Hooks> = {
  hooks: {
    // afterAllInstalled(project, {cache, report}) {

    // }
  },
  configuration: {

  },
  linkers: [
    NodeModulesLinker,
  ],
};

// eslint-disable-next-line arca/no-default-export
export default plugin;
