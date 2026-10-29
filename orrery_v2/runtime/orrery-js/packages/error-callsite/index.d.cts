/// <reference types="node" />
declare function errorCallsite(error: Error): NodeJS.CallSite[] | undefined;
export = errorCallsite;
