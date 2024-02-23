/* eslint-disable */
//prettier-ignore
module.exports = {
name: "@yarnpkg/plugin-fuse",
factory: function (require) {
var plugin=(()=>{var o=Object.defineProperty;var s=Object.getOwnPropertyDescriptor;var p=Object.getOwnPropertyNames;var m=Object.prototype.hasOwnProperty;var k=(i,n)=>{for(var e in n)o(i,e,{get:n[e],enumerable:!0})},c=(i,n,e,a)=>{if(n&&typeof n=="object"||typeof n=="function")for(let r of p(n))!m.call(i,r)&&r!==e&&o(i,r,{get:()=>n[r],enumerable:!(a=s(n,r))||a.enumerable});return i};var g=i=>c(o({},"__esModule",{value:!0}),i);var L={};k(L,{default:()=>d});var t=class{},l=class{supportsPackage(n,e){return this.isEnabled(e)}isEnabled(n){return console.error(n.project.configuration.get("nodeLinker")),n.project.configuration.get("nodeLinker")==="fuse"}makeInstaller(n){return new t}},u={linkers:[l],commands:[]},d=u;return g(L);})();
return plugin;
}
};
