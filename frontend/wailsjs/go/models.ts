export namespace catalog {
	
	export class Issue {
	    id: number;
	    kind: string;
	    msg: msg.Msg;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Issue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.msg = this.convertValues(source["msg"], msg.Msg);
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Root {
	    id: number;
	    path: string;
	    kind: string;
	    creatorLevel: boolean;
	    status: string;
	    lastScanAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new Root(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.creatorLevel = source["creatorLevel"];
	        this.status = source["status"];
	        this.lastScanAt = source["lastScanAt"];
	    }
	}
	export class RootStats {
	    files: number;
	    bytes: number;
	    missing: number;
	    unhashed: number;
	    archives: number;
	    archivesBroken: number;
	    archivesEncrypted: number;
	    archivesPending: number;
	    archiveMembers: number;
	    duplicateFiles: number;
	
	    static createFrom(source: any = {}) {
	        return new RootStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.bytes = source["bytes"];
	        this.missing = source["missing"];
	        this.unhashed = source["unhashed"];
	        this.archives = source["archives"];
	        this.archivesBroken = source["archivesBroken"];
	        this.archivesEncrypted = source["archivesEncrypted"];
	        this.archivesPending = source["archivesPending"];
	        this.archiveMembers = source["archiveMembers"];
	        this.duplicateFiles = source["duplicateFiles"];
	    }
	}

}

export namespace main {
	
	export class AppStatus {
	    dataDir: string;
	    language: string;
	    startupError?: msg.Msg;
	
	    static createFrom(source: any = {}) {
	        return new AppStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataDir = source["dataDir"];
	        this.language = source["language"];
	        this.startupError = this.convertValues(source["startupError"], msg.Msg);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace msg {
	
	export class Msg {
	    key: string;
	    params?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Msg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.params = source["params"];
	    }
	}

}

export namespace scanner {
	
	export class Progress {
	    rootId: number;
	    runId: number;
	    phase: string;
	    status: string;
	    current: string;
	    running: boolean;
	    startedAt: number;
	    finishedAt?: number;
	    filesSeen: number;
	    filesNew: number;
	    filesChanged: number;
	    filesMissing: number;
	    hashTotal: number;
	    hashDone: number;
	    bytesTotal: number;
	    bytesDone: number;
	    archivesTotal: number;
	    archivesDone: number;
	    archivesBroken: number;
	    archivesEncrypted: number;
	    issues: number;
	    error?: msg.Msg;
	
	    static createFrom(source: any = {}) {
	        return new Progress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootId = source["rootId"];
	        this.runId = source["runId"];
	        this.phase = source["phase"];
	        this.status = source["status"];
	        this.current = source["current"];
	        this.running = source["running"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.filesSeen = source["filesSeen"];
	        this.filesNew = source["filesNew"];
	        this.filesChanged = source["filesChanged"];
	        this.filesMissing = source["filesMissing"];
	        this.hashTotal = source["hashTotal"];
	        this.hashDone = source["hashDone"];
	        this.bytesTotal = source["bytesTotal"];
	        this.bytesDone = source["bytesDone"];
	        this.archivesTotal = source["archivesTotal"];
	        this.archivesDone = source["archivesDone"];
	        this.archivesBroken = source["archivesBroken"];
	        this.archivesEncrypted = source["archivesEncrypted"];
	        this.issues = source["issues"];
	        this.error = this.convertValues(source["error"], msg.Msg);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

