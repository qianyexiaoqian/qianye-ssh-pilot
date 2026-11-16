export namespace agent {
	
	export class ApprovalRequest {
	    id: string;
	    sessionId: string;
	    toolCallId: string;
	    tool: string;
	    title: string;
	    detail: string;
	    servers: string[];
	    risk: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.toolCallId = source["toolCallId"];
	        this.tool = source["tool"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.servers = source["servers"];
	        this.risk = source["risk"];
	        this.reason = source["reason"];
	    }
	}
	export class Block {
	    type: string;
	    text?: string;
	    thinking?: string;
	    signature?: string;
	    data?: string;
	    id?: string;
	    name?: string;
	    input?: number[];
	
	    static createFrom(source: any = {}) {
	        return new Block(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.text = source["text"];
	        this.thinking = source["thinking"];
	        this.signature = source["signature"];
	        this.data = source["data"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.input = source["input"];
	    }
	}
	export class IndexStatus {
	    mode: string;
	    model: string;
	    dims: number;
	    facts: number;
	    episodes: number;
	    indexed: number;
	    total: number;
	    indexing: boolean;
	    error: string;
	    threshold: number;
	    related: number;
	    unrelated: number;
	
	    static createFrom(source: any = {}) {
	        return new IndexStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.model = source["model"];
	        this.dims = source["dims"];
	        this.facts = source["facts"];
	        this.episodes = source["episodes"];
	        this.indexed = source["indexed"];
	        this.total = source["total"];
	        this.indexing = source["indexing"];
	        this.error = source["error"];
	        this.threshold = source["threshold"];
	        this.related = source["related"];
	        this.unrelated = source["unrelated"];
	    }
	}
	export class LearnEvent {
	    time: number;
	    source: string;
	    kind: string;
	    action: string;
	    target: string;
	    summary: string;
	    sessionId: string;
	
	    static createFrom(source: any = {}) {
	        return new LearnEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.source = source["source"];
	        this.kind = source["kind"];
	        this.action = source["action"];
	        this.target = source["target"];
	        this.summary = source["summary"];
	        this.sessionId = source["sessionId"];
	    }
	}
	export class MemoryView {
	    target: string;
	    label: string;
	    entries: string[];
	    used: number;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new MemoryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.label = source["label"];
	        this.entries = source["entries"];
	        this.used = source["used"];
	        this.limit = source["limit"];
	    }
	}
	export class ToolCall {
	    id: string;
	    name: string;
	    arguments: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	    }
	}
	export class Message {
	    id: string;
	    role: string;
	    content: string;
	    display?: string;
	    thinking?: string;
	    toolCalls?: ToolCall[];
	    toolCallId?: string;
	    toolName?: string;
	    isError?: boolean;
	    meta?: any;
	    blocks?: Block[];
	    provider?: string;
	    model?: string;
	    kind?: string;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.display = source["display"];
	        this.thinking = source["thinking"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.toolCallId = source["toolCallId"];
	        this.toolName = source["toolName"];
	        this.isError = source["isError"];
	        this.meta = source["meta"];
	        this.blocks = this.convertValues(source["blocks"], Block);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.kind = source["kind"];
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
	export class SessionSummary {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    messages: number;
	    preview: string;
	    learned: number;
	    running: boolean;
	    serverId: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.messages = source["messages"];
	        this.preview = source["preview"];
	        this.learned = source["learned"];
	        this.running = source["running"];
	        this.serverId = source["serverId"];
	    }
	}
	export class Skill {
	    name: string;
	    description: string;
	    version: number;
	    tags: string[];
	    createdBy: string;
	    updatedAt: string;
	    uses: number;
	    lastUsed: number;
	    pinned: boolean;
	    stale: boolean;
	    archivedAt: number;
	    content: string;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new Skill(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.tags = source["tags"];
	        this.createdBy = source["createdBy"];
	        this.updatedAt = source["updatedAt"];
	        this.uses = source["uses"];
	        this.lastUsed = source["lastUsed"];
	        this.pinned = source["pinned"];
	        this.stale = source["stale"];
	        this.archivedAt = source["archivedAt"];
	        this.content = source["content"];
	        this.body = source["body"];
	    }
	}
	export class SkillMeta {
	    name: string;
	    description: string;
	    version: number;
	    tags: string[];
	    createdBy: string;
	    updatedAt: string;
	    uses: number;
	    lastUsed: number;
	    pinned: boolean;
	    stale: boolean;
	    archivedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new SkillMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.tags = source["tags"];
	        this.createdBy = source["createdBy"];
	        this.updatedAt = source["updatedAt"];
	        this.uses = source["uses"];
	        this.lastUsed = source["lastUsed"];
	        this.pinned = source["pinned"];
	        this.stale = source["stale"];
	        this.archivedAt = source["archivedAt"];
	    }
	}

}

export namespace certs {
	
	export class Parsed {
	    domains: string[];
	    subject: string;
	    issuer: string;
	    serial: string;
	    notBefore: number;
	    notAfter: number;
	    fingerprint: string;
	    keyType: string;
	    chainCount: number;
	    selfSigned: boolean;
	    hasKey: boolean;
	    keyMatches: boolean;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new Parsed(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domains = source["domains"];
	        this.subject = source["subject"];
	        this.issuer = source["issuer"];
	        this.serial = source["serial"];
	        this.notBefore = source["notBefore"];
	        this.notAfter = source["notAfter"];
	        this.fingerprint = source["fingerprint"];
	        this.keyType = source["keyType"];
	        this.chainCount = source["chainCount"];
	        this.selfSigned = source["selfSigned"];
	        this.hasKey = source["hasKey"];
	        this.keyMatches = source["keyMatches"];
	        this.warnings = source["warnings"];
	    }
	}
	export class TLSInfo {
	    address: string;
	    sni: string;
	    domains: string[];
	    subject: string;
	    issuer: string;
	    notBefore: number;
	    notAfter: number;
	    fingerprint: string;
	    protocol: string;
	    chainCount: number;
	    trusted: boolean;
	    verifyError: string;
	    latencyMs: number;
	
	    static createFrom(source: any = {}) {
	        return new TLSInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.sni = source["sni"];
	        this.domains = source["domains"];
	        this.subject = source["subject"];
	        this.issuer = source["issuer"];
	        this.notBefore = source["notBefore"];
	        this.notAfter = source["notAfter"];
	        this.fingerprint = source["fingerprint"];
	        this.protocol = source["protocol"];
	        this.chainCount = source["chainCount"];
	        this.trusted = source["trusted"];
	        this.verifyError = source["verifyError"];
	        this.latencyMs = source["latencyMs"];
	    }
	}

}

export namespace main {
	
	export class AppInfo {
	    version: string;
	    dataDir: string;
	    initError: string;
	    secretErrors: number;
	    platform: string;
	    product: string;
	    author: string;
	    window: string;
	    windowServer: string;
	    dataMode: string;
	    backupDir: string;
	    migratedFrom: string;
	    justMigrated: boolean;
	    previousDataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.dataDir = source["dataDir"];
	        this.initError = source["initError"];
	        this.secretErrors = source["secretErrors"];
	        this.platform = source["platform"];
	        this.product = source["product"];
	        this.author = source["author"];
	        this.window = source["window"];
	        this.windowServer = source["windowServer"];
	        this.dataMode = source["dataMode"];
	        this.backupDir = source["backupDir"];
	        this.migratedFrom = source["migratedFrom"];
	        this.justMigrated = source["justMigrated"];
	        this.previousDataDir = source["previousDataDir"];
	    }
	}
	export class AuthorizedKeyView {
	    line: string;
	    type: string;
	    fingerprint: string;
	    comment: string;
	    options: string;
	    blob: string;
	    publicKeyId: string;
	    publicKeyName: string;
	    isLoginKey: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AuthorizedKeyView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.type = source["type"];
	        this.fingerprint = source["fingerprint"];
	        this.comment = source["comment"];
	        this.options = source["options"];
	        this.blob = source["blob"];
	        this.publicKeyId = source["publicKeyId"];
	        this.publicKeyName = source["publicKeyName"];
	        this.isLoginKey = source["isLoginKey"];
	    }
	}
	export class AutoSyncResult {
	    certId: string;
	    certName: string;
	    ok: boolean;
	    changed: boolean;
	    deployed: number;
	    failed: number;
	    message: string;
	    time: number;
	
	    static createFrom(source: any = {}) {
	        return new AutoSyncResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.certId = source["certId"];
	        this.certName = source["certName"];
	        this.ok = source["ok"];
	        this.changed = source["changed"];
	        this.deployed = source["deployed"];
	        this.failed = source["failed"];
	        this.message = source["message"];
	        this.time = source["time"];
	    }
	}
	export class BindingView {
	    serverId: string;
	    serverName: string;
	    boundAt: number;
	
	    static createFrom(source: any = {}) {
	        return new BindingView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.boundAt = source["boundAt"];
	    }
	}
	export class CertBrief {
	    id: string;
	    name: string;
	    domains: string[];
	    notAfter: number;
	    days: number;
	    targets: number;
	    outOfSync: number;
	    failed: number;
	
	    static createFrom(source: any = {}) {
	        return new CertBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.domains = source["domains"];
	        this.notAfter = source["notAfter"];
	        this.days = source["days"];
	        this.targets = source["targets"];
	        this.outOfSync = source["outOfSync"];
	        this.failed = source["failed"];
	    }
	}
	export class TargetView {
	    id: string;
	    certId: string;
	    serverId: string;
	    preset: string;
	    certPath: string;
	    keyPath: string;
	    chainPath: string;
	    certFormat: string;
	    certMode: string;
	    keyMode: string;
	    owner: string;
	    backup: boolean;
	    testCommand: string;
	    services: string[];
	    serviceAction: string;
	    postCommand: string;
	    verifyPort: number;
	    verifyDomain: string;
	    lastDeployAt: number;
	    lastStatus: string;
	    lastMessage: string;
	    lastFingerprint: string;
	    serverName: string;
	    serverHost: string;
	    synced: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TargetView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.certId = source["certId"];
	        this.serverId = source["serverId"];
	        this.preset = source["preset"];
	        this.certPath = source["certPath"];
	        this.keyPath = source["keyPath"];
	        this.chainPath = source["chainPath"];
	        this.certFormat = source["certFormat"];
	        this.certMode = source["certMode"];
	        this.keyMode = source["keyMode"];
	        this.owner = source["owner"];
	        this.backup = source["backup"];
	        this.testCommand = source["testCommand"];
	        this.services = source["services"];
	        this.serviceAction = source["serviceAction"];
	        this.postCommand = source["postCommand"];
	        this.verifyPort = source["verifyPort"];
	        this.verifyDomain = source["verifyDomain"];
	        this.lastDeployAt = source["lastDeployAt"];
	        this.lastStatus = source["lastStatus"];
	        this.lastMessage = source["lastMessage"];
	        this.lastFingerprint = source["lastFingerprint"];
	        this.serverName = source["serverName"];
	        this.serverHost = source["serverHost"];
	        this.synced = source["synced"];
	    }
	}
	export class CertView {
	    id: string;
	    name: string;
	    domains: string[];
	    subject: string;
	    issuer: string;
	    serial: string;
	    notBefore: number;
	    notAfter: number;
	    fingerprint: string;
	    keyType: string;
	    chainCount: number;
	    certPem: string;
	    keyPem?: string;
	    hasKey: boolean;
	    source?: store.CertSource;
	    notes: string;
	    history: store.CertVersion[];
	    autoSync: boolean;
	    autoSyncAt: number;
	    autoSyncOk: boolean;
	    autoSyncMessage: string;
	    createdAt: number;
	    updatedAt: number;
	    days: number;
	    targets: TargetView[];
	    sourceServerName: string;
	
	    static createFrom(source: any = {}) {
	        return new CertView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.domains = source["domains"];
	        this.subject = source["subject"];
	        this.issuer = source["issuer"];
	        this.serial = source["serial"];
	        this.notBefore = source["notBefore"];
	        this.notAfter = source["notAfter"];
	        this.fingerprint = source["fingerprint"];
	        this.keyType = source["keyType"];
	        this.chainCount = source["chainCount"];
	        this.certPem = source["certPem"];
	        this.keyPem = source["keyPem"];
	        this.hasKey = source["hasKey"];
	        this.source = this.convertValues(source["source"], store.CertSource);
	        this.notes = source["notes"];
	        this.history = this.convertValues(source["history"], store.CertVersion);
	        this.autoSync = source["autoSync"];
	        this.autoSyncAt = source["autoSyncAt"];
	        this.autoSyncOk = source["autoSyncOk"];
	        this.autoSyncMessage = source["autoSyncMessage"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.days = source["days"];
	        this.targets = this.convertValues(source["targets"], TargetView);
	        this.sourceServerName = source["sourceServerName"];
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
	export class GroupCount {
	    name: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new GroupCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.count = source["count"];
	    }
	}
	export class Dashboard {
	    serverCount: number;
	    onlineCount: number;
	    offlineCount: number;
	    keyCount: number;
	    publicKeyCount: number;
	    bindingCount: number;
	    certCount: number;
	    certExpiring: number;
	    certExpired: number;
	    scriptCount: number;
	    skillCount: number;
	    sessionCount: number;
	    memory: agent.MemoryView;
	    userMemory: agent.MemoryView;
	    longMemory: agent.IndexStatus;
	    certs: CertBrief[];
	    groups: GroupCount[];
	    recentRuns: store.RunRecord[];
	    recentLearning: agent.LearnEvent[];
	    lastReport?: ops.Report;
	    aiConfigured: boolean;
	    aiModel: string;
	
	    static createFrom(source: any = {}) {
	        return new Dashboard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverCount = source["serverCount"];
	        this.onlineCount = source["onlineCount"];
	        this.offlineCount = source["offlineCount"];
	        this.keyCount = source["keyCount"];
	        this.publicKeyCount = source["publicKeyCount"];
	        this.bindingCount = source["bindingCount"];
	        this.certCount = source["certCount"];
	        this.certExpiring = source["certExpiring"];
	        this.certExpired = source["certExpired"];
	        this.scriptCount = source["scriptCount"];
	        this.skillCount = source["skillCount"];
	        this.sessionCount = source["sessionCount"];
	        this.memory = this.convertValues(source["memory"], agent.MemoryView);
	        this.userMemory = this.convertValues(source["userMemory"], agent.MemoryView);
	        this.longMemory = this.convertValues(source["longMemory"], agent.IndexStatus);
	        this.certs = this.convertValues(source["certs"], CertBrief);
	        this.groups = this.convertValues(source["groups"], GroupCount);
	        this.recentRuns = this.convertValues(source["recentRuns"], store.RunRecord);
	        this.recentLearning = this.convertValues(source["recentLearning"], agent.LearnEvent);
	        this.lastReport = this.convertValues(source["lastReport"], ops.Report);
	        this.aiConfigured = source["aiConfigured"];
	        this.aiModel = source["aiModel"];
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
	export class DiscoveredView {
	    source: string;
	    certPath: string;
	    keyPath: string;
	    domains: string[];
	    issuer: string;
	    notAfter: number;
	    fingerprint: string;
	    error: string;
	    matchCertId: string;
	    matchCertName: string;
	    sameVersion: boolean;
	    hasTarget: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DiscoveredView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.certPath = source["certPath"];
	        this.keyPath = source["keyPath"];
	        this.domains = source["domains"];
	        this.issuer = source["issuer"];
	        this.notAfter = source["notAfter"];
	        this.fingerprint = source["fingerprint"];
	        this.error = source["error"];
	        this.matchCertId = source["matchCertId"];
	        this.matchCertName = source["matchCertName"];
	        this.sameVersion = source["sameVersion"];
	        this.hasTarget = source["hasTarget"];
	    }
	}
	export class EmbedTest {
	    model: string;
	    dims: number;
	    latencyMs: number;
	    related: number;
	    unrelated: number;
	    threshold: number;
	    warning: string;
	
	    static createFrom(source: any = {}) {
	        return new EmbedTest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.dims = source["dims"];
	        this.latencyMs = source["latencyMs"];
	        this.related = source["related"];
	        this.unrelated = source["unrelated"];
	        this.threshold = source["threshold"];
	        this.warning = source["warning"];
	    }
	}
	export class FileContent {
	    path: string;
	    name: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new FileContent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.content = source["content"];
	    }
	}
	export class FileListing {
	    path: string;
	    parent: string;
	    entries: sshx.FileEntry[];
	
	    static createFrom(source: any = {}) {
	        return new FileListing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.parent = source["parent"];
	        this.entries = this.convertValues(source["entries"], sshx.FileEntry);
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
	export class FileText {
	    path: string;
	    content: string;
	    size: number;
	    perm: string;
	    viaSudo: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileText(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.size = source["size"];
	        this.perm = source["perm"];
	        this.viaSudo = source["viaSudo"];
	    }
	}
	export class GenerateKeyRequest {
	    name: string;
	    kind: string;
	    bits: number;
	    comment: string;
	    passphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new GenerateKeyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.bits = source["bits"];
	        this.comment = source["comment"];
	        this.passphrase = source["passphrase"];
	    }
	}
	
	export class ImportCertRequest {
	    id: string;
	    name: string;
	    certPem: string;
	    keyPem: string;
	    chainPem: string;
	    notes: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportCertRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.certPem = source["certPem"];
	        this.keyPem = source["keyPem"];
	        this.chainPem = source["chainPem"];
	        this.notes = source["notes"];
	    }
	}
	export class ImportKeyRequest {
	    name: string;
	    pem: string;
	    passphrase: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportKeyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.pem = source["pem"];
	        this.passphrase = source["passphrase"];
	    }
	}
	export class KeyView {
	    id: string;
	    name: string;
	    type: string;
	    privatePem?: string;
	    passphrase?: string;
	    encrypted: boolean;
	    hasPassphrase: boolean;
	    publicKey: string;
	    fingerprint: string;
	    comment: string;
	    createdAt: number;
	    publicKeyId: string;
	    usedBy: string[];
	
	    static createFrom(source: any = {}) {
	        return new KeyView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.privatePem = source["privatePem"];
	        this.passphrase = source["passphrase"];
	        this.encrypted = source["encrypted"];
	        this.hasPassphrase = source["hasPassphrase"];
	        this.publicKey = source["publicKey"];
	        this.fingerprint = source["fingerprint"];
	        this.comment = source["comment"];
	        this.createdAt = source["createdAt"];
	        this.publicKeyId = source["publicKeyId"];
	        this.usedBy = source["usedBy"];
	    }
	}
	export class LongItem {
	    id: string;
	    kind: string;
	    text: string;
	    servers: string[];
	    serverNames: string[];
	    sessionId: string;
	    title: string;
	    source: string;
	    createdAt: number;
	    updatedAt: number;
	    hits: number;
	    lastHit: number;
	    score: number;
	
	    static createFrom(source: any = {}) {
	        return new LongItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.servers = source["servers"];
	        this.serverNames = source["serverNames"];
	        this.sessionId = source["sessionId"];
	        this.title = source["title"];
	        this.source = source["source"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.hits = source["hits"];
	        this.lastHit = source["lastHit"];
	        this.score = source["score"];
	    }
	}
	export class OpResult {
	    serverId: string;
	    serverName: string;
	    ok: boolean;
	    message: string;
	    output?: string;
	
	    static createFrom(source: any = {}) {
	        return new OpResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.output = source["output"];
	    }
	}
	export class PublicKeyView {
	    id: string;
	    name: string;
	    key: string;
	    type: string;
	    fingerprint: string;
	    comment: string;
	    privateKeyId: string;
	    createdAt: number;
	    bindings: BindingView[];
	
	    static createFrom(source: any = {}) {
	        return new PublicKeyView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.key = source["key"];
	        this.type = source["type"];
	        this.fingerprint = source["fingerprint"];
	        this.comment = source["comment"];
	        this.privateKeyId = source["privateKeyId"];
	        this.createdAt = source["createdAt"];
	        this.bindings = this.convertValues(source["bindings"], BindingView);
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
	export class PullCertRequest {
	    certId: string;
	    serverId: string;
	    certPath: string;
	    keyPath: string;
	    chainPath: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new PullCertRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.certId = source["certId"];
	        this.serverId = source["serverId"];
	        this.certPath = source["certPath"];
	        this.keyPath = source["keyPath"];
	        this.chainPath = source["chainPath"];
	        this.name = source["name"];
	    }
	}
	export class RefreshResult {
	    changed: boolean;
	    cert: CertView;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new RefreshResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.changed = source["changed"];
	        this.cert = this.convertValues(source["cert"], CertView);
	        this.message = source["message"];
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
	export class RunRequest {
	    scriptId: string;
	    content: string;
	    interpreter: string;
	    serverIds: string[];
	    params: Record<string, string>;
	    title: string;
	    timeoutSec: number;
	
	    static createFrom(source: any = {}) {
	        return new RunRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scriptId = source["scriptId"];
	        this.content = source["content"];
	        this.interpreter = source["interpreter"];
	        this.serverIds = source["serverIds"];
	        this.params = source["params"];
	        this.title = source["title"];
	        this.timeoutSec = source["timeoutSec"];
	    }
	}
	export class ServerInput {
	    id: string;
	    name: string;
	    host: string;
	    port: number;
	    username: string;
	    authType: string;
	    keyId: string;
	    password: string;
	    clearPassword: boolean;
	    group: string;
	    tags: string[];
	    jumpServerId: string;
	    useSudo: boolean;
	    sudoPassword: string;
	    clearSudoPassword: boolean;
	    notes: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.authType = source["authType"];
	        this.keyId = source["keyId"];
	        this.password = source["password"];
	        this.clearPassword = source["clearPassword"];
	        this.group = source["group"];
	        this.tags = source["tags"];
	        this.jumpServerId = source["jumpServerId"];
	        this.useSudo = source["useSudo"];
	        this.sudoPassword = source["sudoPassword"];
	        this.clearSudoPassword = source["clearSudoPassword"];
	        this.notes = source["notes"];
	    }
	}
	export class SessionView {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    messages: agent.Message[];
	    running: boolean;
	    pending: agent.ApprovalRequest[];
	    inputTokens: number;
	    outputTokens: number;
	    approveAll: boolean;
	    learned: number;
	    serverId: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.messages = this.convertValues(source["messages"], agent.Message);
	        this.running = source["running"];
	        this.pending = this.convertValues(source["pending"], agent.ApprovalRequest);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.approveAll = source["approveAll"];
	        this.learned = source["learned"];
	        this.serverId = source["serverId"];
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
	
	export class TestResult {
	    ok: boolean;
	    latencyMs: number;
	    message: string;
	    os: string;
	    hostKeyFingerprint: string;
	    hostKeyNew: boolean;
	    mismatch: boolean;
	    sudoOk: boolean;
	    sudoChecked: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.latencyMs = source["latencyMs"];
	        this.message = source["message"];
	        this.os = source["os"];
	        this.hostKeyFingerprint = source["hostKeyFingerprint"];
	        this.hostKeyNew = source["hostKeyNew"];
	        this.mismatch = source["mismatch"];
	        this.sudoOk = source["sudoOk"];
	        this.sudoChecked = source["sudoChecked"];
	    }
	}
	export class Transfer {
	    id: string;
	    serverId: string;
	    serverName: string;
	    kind: string;
	    title: string;
	    target: string;
	    total: number;
	    done: number;
	    files: number;
	    filesDone: number;
	    current: string;
	    skipped: number;
	    status: string;
	    error: string;
	    speed: number;
	    startedAt: number;
	    finishedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Transfer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.target = source["target"];
	        this.total = source["total"];
	        this.done = source["done"];
	        this.files = source["files"];
	        this.filesDone = source["filesDone"];
	        this.current = source["current"];
	        this.skipped = source["skipped"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.speed = source["speed"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	    }
	}
	export class UploadStart {
	    id: string;
	    conflicts: string[];
	
	    static createFrom(source: any = {}) {
	        return new UploadStart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conflicts = source["conflicts"];
	    }
	}

}

export namespace ops {
	
	export class Action {
	    type: string;
	    label: string;
	    scriptId?: string;
	    params?: Record<string, string>;
	    command?: string;
	    certId?: string;
	    service?: string;
	    prompt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.label = source["label"];
	        this.scriptId = source["scriptId"];
	        this.params = source["params"];
	        this.command = source["command"];
	        this.certId = source["certId"];
	        this.service = source["service"];
	        this.prompt = source["prompt"];
	    }
	}
	export class DiskInfo {
	    mount: string;
	    device: string;
	    size: number;
	    used: number;
	    usePct: number;
	
	    static createFrom(source: any = {}) {
	        return new DiskInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mount = source["mount"];
	        this.device = source["device"];
	        this.size = source["size"];
	        this.used = source["used"];
	        this.usePct = source["usePct"];
	    }
	}
	export class Finding {
	    id: string;
	    severity: string;
	    category: string;
	    serverId: string;
	    serverName: string;
	    title: string;
	    detail: string;
	    suggestion: string;
	    action?: Action;
	
	    static createFrom(source: any = {}) {
	        return new Finding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.severity = source["severity"];
	        this.category = source["category"];
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.suggestion = source["suggestion"];
	        this.action = this.convertValues(source["action"], Action);
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
	export class ProcInfo {
	    pid: number;
	    command: string;
	    cpu: number;
	    mem: number;
	
	    static createFrom(source: any = {}) {
	        return new ProcInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.command = source["command"];
	        this.cpu = source["cpu"];
	        this.mem = source["mem"];
	    }
	}
	export class ServerInfo {
	    hostname: string;
	    os: string;
	    kernel: string;
	    arch: string;
	    cpuModel: string;
	    cpus: number;
	    cpuUsage: number;
	    uptime: number;
	    load: number[];
	    memTotal: number;
	    memAvailable: number;
	    swapTotal: number;
	    swapFree: number;
	    disks: DiskInfo[];
	    ips: string[];
	    failedUnits: string[];
	    procs: number;
	    users: number;
	    topProcs: ProcInfo[];
	    collectedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new ServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hostname = source["hostname"];
	        this.os = source["os"];
	        this.kernel = source["kernel"];
	        this.arch = source["arch"];
	        this.cpuModel = source["cpuModel"];
	        this.cpus = source["cpus"];
	        this.cpuUsage = source["cpuUsage"];
	        this.uptime = source["uptime"];
	        this.load = source["load"];
	        this.memTotal = source["memTotal"];
	        this.memAvailable = source["memAvailable"];
	        this.swapTotal = source["swapTotal"];
	        this.swapFree = source["swapFree"];
	        this.disks = this.convertValues(source["disks"], DiskInfo);
	        this.ips = source["ips"];
	        this.failedUnits = source["failedUnits"];
	        this.procs = source["procs"];
	        this.users = source["users"];
	        this.topProcs = this.convertValues(source["topProcs"], ProcInfo);
	        this.collectedAt = source["collectedAt"];
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
	export class ServerReport {
	    serverId: string;
	    serverName: string;
	    online: boolean;
	    error: string;
	    info?: ServerInfo;
	
	    static createFrom(source: any = {}) {
	        return new ServerReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.online = source["online"];
	        this.error = source["error"];
	        this.info = this.convertValues(source["info"], ServerInfo);
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
	export class Report {
	    id: string;
	    startedAt: number;
	    finishedAt: number;
	    score: number;
	    servers: ServerReport[];
	    findings: Finding[];
	    critical: number;
	    warning: number;
	    info: number;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.score = source["score"];
	        this.servers = this.convertValues(source["servers"], ServerReport);
	        this.findings = this.convertValues(source["findings"], Finding);
	        this.critical = source["critical"];
	        this.warning = source["warning"];
	        this.info = source["info"];
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
	export class Sample {
	    serverId: string;
	    time: number;
	    cpu: number;
	    ioWait: number;
	    cpus: number;
	    load: number[];
	    memTotal: number;
	    memUsed: number;
	    memPct: number;
	    swapTotal: number;
	    swapUsed: number;
	    hasNet: boolean;
	    netRx: number;
	    netTx: number;
	    diskMount: string;
	    diskPct: number;
	    diskUsed: number;
	    diskSize: number;
	    error: string;
	    stopped: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Sample(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.time = source["time"];
	        this.cpu = source["cpu"];
	        this.ioWait = source["ioWait"];
	        this.cpus = source["cpus"];
	        this.load = source["load"];
	        this.memTotal = source["memTotal"];
	        this.memUsed = source["memUsed"];
	        this.memPct = source["memPct"];
	        this.swapTotal = source["swapTotal"];
	        this.swapUsed = source["swapUsed"];
	        this.hasNet = source["hasNet"];
	        this.netRx = source["netRx"];
	        this.netTx = source["netTx"];
	        this.diskMount = source["diskMount"];
	        this.diskPct = source["diskPct"];
	        this.diskUsed = source["diskUsed"];
	        this.diskSize = source["diskSize"];
	        this.error = source["error"];
	        this.stopped = source["stopped"];
	    }
	}
	
	
	export class ServiceInfo {
	    name: string;
	    load: string;
	    active: string;
	    sub: string;
	    enabled: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new ServiceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.load = source["load"];
	        this.active = source["active"];
	        this.sub = source["sub"];
	        this.enabled = source["enabled"];
	        this.description = source["description"];
	    }
	}

}

export namespace sshx {
	
	export class FileEntry {
	    name: string;
	    path: string;
	    size: number;
	    mode: string;
	    perm: string;
	    modTime: number;
	    isDir: boolean;
	    isLink: boolean;
	    linkTarget: string;
	    uid: number;
	    gid: number;
	    owner: string;
	    group: string;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.mode = source["mode"];
	        this.perm = source["perm"];
	        this.modTime = source["modTime"];
	        this.isDir = source["isDir"];
	        this.isLink = source["isLink"];
	        this.linkTarget = source["linkTarget"];
	        this.uid = source["uid"];
	        this.gid = source["gid"];
	        this.owner = source["owner"];
	        this.group = source["group"];
	    }
	}

}

export namespace store {
	
	export class AISettings {
	    provider: string;
	    preset: string;
	    baseUrl: string;
	    apiKey?: string;
	    hasApiKey: boolean;
	    model: string;
	    effort: string;
	    maxTokens: number;
	    approvalMode: string;
	    autoLearn: boolean;
	    nudgeInterval: number;
	    reviewThreshold: number;
	    maxSteps: number;
	    contextChars: number;
	
	    static createFrom(source: any = {}) {
	        return new AISettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.preset = source["preset"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.hasApiKey = source["hasApiKey"];
	        this.model = source["model"];
	        this.effort = source["effort"];
	        this.maxTokens = source["maxTokens"];
	        this.approvalMode = source["approvalMode"];
	        this.autoLearn = source["autoLearn"];
	        this.nudgeInterval = source["nudgeInterval"];
	        this.reviewThreshold = source["reviewThreshold"];
	        this.maxSteps = source["maxSteps"];
	        this.contextChars = source["contextChars"];
	    }
	}
	export class CertSource {
	    serverId: string;
	    certPath: string;
	    keyPath: string;
	    chainPath: string;
	
	    static createFrom(source: any = {}) {
	        return new CertSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.certPath = source["certPath"];
	        this.keyPath = source["keyPath"];
	        this.chainPath = source["chainPath"];
	    }
	}
	export class CertVersion {
	    fingerprint: string;
	    notAfter: number;
	    replacedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new CertVersion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fingerprint = source["fingerprint"];
	        this.notAfter = source["notAfter"];
	        this.replacedAt = source["replacedAt"];
	    }
	}
	export class DeployTarget {
	    id: string;
	    certId: string;
	    serverId: string;
	    preset: string;
	    certPath: string;
	    keyPath: string;
	    chainPath: string;
	    certFormat: string;
	    certMode: string;
	    keyMode: string;
	    owner: string;
	    backup: boolean;
	    testCommand: string;
	    services: string[];
	    serviceAction: string;
	    postCommand: string;
	    verifyPort: number;
	    verifyDomain: string;
	    lastDeployAt: number;
	    lastStatus: string;
	    lastMessage: string;
	    lastFingerprint: string;
	
	    static createFrom(source: any = {}) {
	        return new DeployTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.certId = source["certId"];
	        this.serverId = source["serverId"];
	        this.preset = source["preset"];
	        this.certPath = source["certPath"];
	        this.keyPath = source["keyPath"];
	        this.chainPath = source["chainPath"];
	        this.certFormat = source["certFormat"];
	        this.certMode = source["certMode"];
	        this.keyMode = source["keyMode"];
	        this.owner = source["owner"];
	        this.backup = source["backup"];
	        this.testCommand = source["testCommand"];
	        this.services = source["services"];
	        this.serviceAction = source["serviceAction"];
	        this.postCommand = source["postCommand"];
	        this.verifyPort = source["verifyPort"];
	        this.verifyDomain = source["verifyDomain"];
	        this.lastDeployAt = source["lastDeployAt"];
	        this.lastStatus = source["lastStatus"];
	        this.lastMessage = source["lastMessage"];
	        this.lastFingerprint = source["lastFingerprint"];
	    }
	}
	export class EmbedSettings {
	    preset: string;
	    baseUrl: string;
	    apiKey?: string;
	    hasApiKey: boolean;
	    model: string;
	    dimensions: number;
	    recallOff: boolean;
	    recallCount: number;
	    minScore: number;
	
	    static createFrom(source: any = {}) {
	        return new EmbedSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preset = source["preset"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.hasApiKey = source["hasApiKey"];
	        this.model = source["model"];
	        this.dimensions = source["dimensions"];
	        this.recallOff = source["recallOff"];
	        this.recallCount = source["recallCount"];
	        this.minScore = source["minScore"];
	    }
	}
	export class GeneralSettings {
	    theme: string;
	    statusInterval: number;
	    concurrency: number;
	    commandTimeout: number;
	    certWarnDays: number;
	    monitorInterval: number;
	    monitorOff: boolean;
	    inspectInterval: number;
	
	    static createFrom(source: any = {}) {
	        return new GeneralSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.statusInterval = source["statusInterval"];
	        this.concurrency = source["concurrency"];
	        this.commandTimeout = source["commandTimeout"];
	        this.certWarnDays = source["certWarnDays"];
	        this.monitorInterval = source["monitorInterval"];
	        this.monitorOff = source["monitorOff"];
	        this.inspectInterval = source["inspectInterval"];
	    }
	}
	export class PublicKey {
	    id: string;
	    name: string;
	    key: string;
	    type: string;
	    fingerprint: string;
	    comment: string;
	    privateKeyId: string;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new PublicKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.key = source["key"];
	        this.type = source["type"];
	        this.fingerprint = source["fingerprint"];
	        this.comment = source["comment"];
	        this.privateKeyId = source["privateKeyId"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class QuickCommand {
	    id: string;
	    name: string;
	    command: string;
	    group: string;
	    description: string;
	    autoEnter: boolean;
	    confirm: boolean;
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new QuickCommand(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.group = source["group"];
	        this.description = source["description"];
	        this.autoEnter = source["autoEnter"];
	        this.confirm = source["confirm"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class RunResult {
	    serverId: string;
	    serverName: string;
	    exitCode: number;
	    output: string;
	    error: string;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new RunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverId = source["serverId"];
	        this.serverName = source["serverName"];
	        this.exitCode = source["exitCode"];
	        this.output = source["output"];
	        this.error = source["error"];
	        this.durationMs = source["durationMs"];
	    }
	}
	export class RunRecord {
	    id: string;
	    kind: string;
	    title: string;
	    scriptId: string;
	    command: string;
	    status: string;
	    startedAt: number;
	    finishedAt: number;
	    results: RunResult[];
	
	    static createFrom(source: any = {}) {
	        return new RunRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.scriptId = source["scriptId"];
	        this.command = source["command"];
	        this.status = source["status"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.results = this.convertValues(source["results"], RunResult);
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
	
	export class ScriptParam {
	    name: string;
	    label: string;
	    default: string;
	    required: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScriptParam(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.label = source["label"];
	        this.default = source["default"];
	        this.required = source["required"];
	    }
	}
	export class Script {
	    id: string;
	    name: string;
	    description: string;
	    category: string;
	    interpreter: string;
	    content: string;
	    params: ScriptParam[];
	    tags: string[];
	    readOnly: boolean;
	    builtin: boolean;
	    createdBy: string;
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Script(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.interpreter = source["interpreter"];
	        this.content = source["content"];
	        this.params = this.convertValues(source["params"], ScriptParam);
	        this.tags = source["tags"];
	        this.readOnly = source["readOnly"];
	        this.builtin = source["builtin"];
	        this.createdBy = source["createdBy"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
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
	
	export class Server {
	    id: string;
	    name: string;
	    host: string;
	    port: number;
	    username: string;
	    authType: string;
	    keyId: string;
	    password?: string;
	    hasPassword: boolean;
	    group: string;
	    tags: string[];
	    jumpServerId: string;
	    useSudo: boolean;
	    sudoPassword?: string;
	    hasSudoPassword: boolean;
	    notes: string;
	    hostKey: string;
	    hostKeyFingerprint: string;
	    os: string;
	    lastStatus: string;
	    lastLatency: number;
	    lastCheckAt: number;
	    lastError: string;
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Server(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.authType = source["authType"];
	        this.keyId = source["keyId"];
	        this.password = source["password"];
	        this.hasPassword = source["hasPassword"];
	        this.group = source["group"];
	        this.tags = source["tags"];
	        this.jumpServerId = source["jumpServerId"];
	        this.useSudo = source["useSudo"];
	        this.sudoPassword = source["sudoPassword"];
	        this.hasSudoPassword = source["hasSudoPassword"];
	        this.notes = source["notes"];
	        this.hostKey = source["hostKey"];
	        this.hostKeyFingerprint = source["hostKeyFingerprint"];
	        this.os = source["os"];
	        this.lastStatus = source["lastStatus"];
	        this.lastLatency = source["lastLatency"];
	        this.lastCheckAt = source["lastCheckAt"];
	        this.lastError = source["lastError"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class TerminalSettings {
	    fontSize: number;
	    fontFamily: string;
	    theme: string;
	    cursorBlink: boolean;
	    scrollback: number;
	    copyOnSelect: boolean;
	    keyCopy: string;
	    keyPaste: string;
	    keyFind: string;
	    plainKeysOff: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TerminalSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fontSize = source["fontSize"];
	        this.fontFamily = source["fontFamily"];
	        this.theme = source["theme"];
	        this.cursorBlink = source["cursorBlink"];
	        this.scrollback = source["scrollback"];
	        this.copyOnSelect = source["copyOnSelect"];
	        this.keyCopy = source["keyCopy"];
	        this.keyPaste = source["keyPaste"];
	        this.keyFind = source["keyFind"];
	        this.plainKeysOff = source["plainKeysOff"];
	    }
	}
	export class Settings {
	    ai: AISettings;
	    embed: EmbedSettings;
	    terminal: TerminalSettings;
	    general: GeneralSettings;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ai = this.convertValues(source["ai"], AISettings);
	        this.embed = this.convertValues(source["embed"], EmbedSettings);
	        this.terminal = this.convertValues(source["terminal"], TerminalSettings);
	        this.general = this.convertValues(source["general"], GeneralSettings);
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

