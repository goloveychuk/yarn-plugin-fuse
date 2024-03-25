import { LinkType } from "@yarnpkg/core";
import { BinSymlinkMap, LocationNode, LocationTree } from "./NodeModulesLinker";
import { FuseData, FuseNode } from "./types";
import {ppath}                               from '@yarnpkg/fslib';

export function buildFuseTree(
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
              children: {},
            };
          }
        }
  
        result.roots[root] = node;
      }
    }
    return result;
  }
  