var OwnerHandoffHost = (() => {
  var __create = Object.create;
  var __defProp = Object.defineProperty;
  var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
  var __getOwnPropNames = Object.getOwnPropertyNames;
  var __getProtoOf = Object.getPrototypeOf;
  var __hasOwnProp = Object.prototype.hasOwnProperty;
  var __commonJS = (cb, mod) => function __require() {
    return mod || (0, cb[__getOwnPropNames(cb)[0]])((mod = { exports: {} }).exports, mod), mod.exports;
  };
  var __copyProps = (to, from, except, desc) => {
    if (from && typeof from === "object" || typeof from === "function") {
      for (let key2 of __getOwnPropNames(from))
        if (!__hasOwnProp.call(to, key2) && key2 !== except)
          __defProp(to, key2, { get: () => from[key2], enumerable: !(desc = __getOwnPropDesc(from, key2)) || desc.enumerable });
    }
    return to;
  };
  var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
    // If the importer is in node compatibility mode or this is not an ESM
    // file that has been converted to a CommonJS file using a Babel-
    // compatible transform (i.e. "__esModule" has not been set), then set
    // "default" to the CommonJS "module.exports" for node compatibility.
    isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
    mod
  ));

  // node_modules/@xmldom/xmldom/lib/conventions.js
  var require_conventions = __commonJS({
    "node_modules/@xmldom/xmldom/lib/conventions.js"(exports) {
      "use strict";
      function find(list, predicate, ac) {
        if (ac === void 0) {
          ac = Array.prototype;
        }
        if (list && typeof ac.find === "function") {
          return ac.find.call(list, predicate);
        }
        for (var i = 0; i < list.length; i++) {
          if (hasOwn(list, i)) {
            var item = list[i];
            if (predicate.call(void 0, item, i, list)) {
              return item;
            }
          }
        }
      }
      function freeze(object, oc) {
        if (oc === void 0) {
          oc = Object;
        }
        if (oc && typeof oc.getOwnPropertyDescriptors === "function") {
          object = oc.create(null, oc.getOwnPropertyDescriptors(object));
        }
        return oc && typeof oc.freeze === "function" ? oc.freeze(object) : object;
      }
      function hasOwn(object, key2) {
        return Object.prototype.hasOwnProperty.call(object, key2);
      }
      function assign(target, source) {
        if (target === null || typeof target !== "object") {
          throw new TypeError("target is not an object");
        }
        for (var key2 in source) {
          if (hasOwn(source, key2)) {
            target[key2] = source[key2];
          }
        }
        return target;
      }
      var HTML_BOOLEAN_ATTRIBUTES = freeze({
        allowfullscreen: true,
        async: true,
        autofocus: true,
        autoplay: true,
        checked: true,
        controls: true,
        default: true,
        defer: true,
        disabled: true,
        formnovalidate: true,
        hidden: true,
        ismap: true,
        itemscope: true,
        loop: true,
        multiple: true,
        muted: true,
        nomodule: true,
        novalidate: true,
        open: true,
        playsinline: true,
        readonly: true,
        required: true,
        reversed: true,
        selected: true
      });
      function isHTMLBooleanAttribute(name) {
        return hasOwn(HTML_BOOLEAN_ATTRIBUTES, name.toLowerCase());
      }
      var HTML_VOID_ELEMENTS = freeze({
        area: true,
        base: true,
        br: true,
        col: true,
        embed: true,
        hr: true,
        img: true,
        input: true,
        link: true,
        meta: true,
        param: true,
        source: true,
        track: true,
        wbr: true
      });
      function isHTMLVoidElement(tagName) {
        return hasOwn(HTML_VOID_ELEMENTS, tagName.toLowerCase());
      }
      var HTML_RAW_TEXT_ELEMENTS = freeze({
        script: false,
        style: false,
        textarea: true,
        title: true
      });
      function isHTMLRawTextElement(tagName) {
        var key2 = tagName.toLowerCase();
        return hasOwn(HTML_RAW_TEXT_ELEMENTS, key2) && !HTML_RAW_TEXT_ELEMENTS[key2];
      }
      function isHTMLEscapableRawTextElement(tagName) {
        var key2 = tagName.toLowerCase();
        return hasOwn(HTML_RAW_TEXT_ELEMENTS, key2) && HTML_RAW_TEXT_ELEMENTS[key2];
      }
      function isHTMLMimeType(mimeType) {
        return mimeType === MIME_TYPE.HTML;
      }
      function hasDefaultHTMLNamespace(mimeType) {
        return isHTMLMimeType(mimeType) || mimeType === MIME_TYPE.XML_XHTML_APPLICATION;
      }
      var MIME_TYPE = freeze({
        /**
         * `text/html`, the only mime type that triggers treating an XML document as HTML.
         *
         * @see https://www.iana.org/assignments/media-types/text/html IANA MimeType registration
         * @see https://en.wikipedia.org/wiki/HTML Wikipedia
         * @see https://developer.mozilla.org/en-US/docs/Web/API/DOMParser/parseFromString MDN
         * @see https://html.spec.whatwg.org/multipage/dynamic-markup-insertion.html#dom-domparser-parsefromstring
         *      WHATWG HTML Spec
         */
        HTML: "text/html",
        /**
         * `application/xml`, the standard mime type for XML documents.
         *
         * @see https://www.iana.org/assignments/media-types/application/xml IANA MimeType
         *      registration
         * @see https://tools.ietf.org/html/rfc7303#section-9.1 RFC 7303
         * @see https://en.wikipedia.org/wiki/XML_and_MIME Wikipedia
         */
        XML_APPLICATION: "application/xml",
        /**
         * `text/xml`, an alias for `application/xml`.
         *
         * @see https://tools.ietf.org/html/rfc7303#section-9.2 RFC 7303
         * @see https://www.iana.org/assignments/media-types/text/xml IANA MimeType registration
         * @see https://en.wikipedia.org/wiki/XML_and_MIME Wikipedia
         */
        XML_TEXT: "text/xml",
        /**
         * `application/xhtml+xml`, indicates an XML document that has the default HTML namespace,
         * but is parsed as an XML document.
         *
         * @see https://www.iana.org/assignments/media-types/application/xhtml+xml IANA MimeType
         *      registration
         * @see https://dom.spec.whatwg.org/#dom-domimplementation-createdocument WHATWG DOM Spec
         * @see https://en.wikipedia.org/wiki/XHTML Wikipedia
         */
        XML_XHTML_APPLICATION: "application/xhtml+xml",
        /**
         * `image/svg+xml`,
         *
         * @see https://www.iana.org/assignments/media-types/image/svg+xml IANA MimeType registration
         * @see https://www.w3.org/TR/SVG11/ W3C SVG 1.1
         * @see https://en.wikipedia.org/wiki/Scalable_Vector_Graphics Wikipedia
         */
        XML_SVG_IMAGE: "image/svg+xml"
      });
      var _MIME_TYPES = Object.keys(MIME_TYPE).map(function(key2) {
        return MIME_TYPE[key2];
      });
      function isValidMimeType(mimeType) {
        return _MIME_TYPES.indexOf(mimeType) > -1;
      }
      var NAMESPACE = freeze({
        /**
         * The XHTML namespace.
         *
         * @see http://www.w3.org/1999/xhtml
         */
        HTML: "http://www.w3.org/1999/xhtml",
        /**
         * The SVG namespace.
         *
         * @see http://www.w3.org/2000/svg
         */
        SVG: "http://www.w3.org/2000/svg",
        /**
         * The `xml:` namespace.
         *
         * @see http://www.w3.org/XML/1998/namespace
         */
        XML: "http://www.w3.org/XML/1998/namespace",
        /**
         * The `xmlns:` namespace.
         *
         * @see https://www.w3.org/2000/xmlns/
         */
        XMLNS: "http://www.w3.org/2000/xmlns/"
      });
      exports.assign = assign;
      exports.find = find;
      exports.freeze = freeze;
      exports.HTML_BOOLEAN_ATTRIBUTES = HTML_BOOLEAN_ATTRIBUTES;
      exports.HTML_RAW_TEXT_ELEMENTS = HTML_RAW_TEXT_ELEMENTS;
      exports.HTML_VOID_ELEMENTS = HTML_VOID_ELEMENTS;
      exports.hasDefaultHTMLNamespace = hasDefaultHTMLNamespace;
      exports.hasOwn = hasOwn;
      exports.isHTMLBooleanAttribute = isHTMLBooleanAttribute;
      exports.isHTMLRawTextElement = isHTMLRawTextElement;
      exports.isHTMLEscapableRawTextElement = isHTMLEscapableRawTextElement;
      exports.isHTMLMimeType = isHTMLMimeType;
      exports.isHTMLVoidElement = isHTMLVoidElement;
      exports.isValidMimeType = isValidMimeType;
      exports.MIME_TYPE = MIME_TYPE;
      exports.NAMESPACE = NAMESPACE;
    }
  });

  // node_modules/@xmldom/xmldom/lib/errors.js
  var require_errors = __commonJS({
    "node_modules/@xmldom/xmldom/lib/errors.js"(exports) {
      "use strict";
      var conventions = require_conventions();
      function extendError(constructor, writableName) {
        constructor.prototype = Object.create(Error.prototype, {
          constructor: { value: constructor },
          name: { value: constructor.name, enumerable: true, writable: writableName }
        });
      }
      var DOMExceptionName = conventions.freeze({
        /**
         * the default value as defined by the spec
         */
        Error: "Error",
        /**
         * @deprecated
         * Use RangeError instead.
         */
        IndexSizeError: "IndexSizeError",
        /**
         * @deprecated
         * Just to match the related static code, not part of the spec.
         */
        DomstringSizeError: "DomstringSizeError",
        HierarchyRequestError: "HierarchyRequestError",
        WrongDocumentError: "WrongDocumentError",
        InvalidCharacterError: "InvalidCharacterError",
        /**
         * @deprecated
         * Just to match the related static code, not part of the spec.
         */
        NoDataAllowedError: "NoDataAllowedError",
        NoModificationAllowedError: "NoModificationAllowedError",
        NotFoundError: "NotFoundError",
        NotSupportedError: "NotSupportedError",
        InUseAttributeError: "InUseAttributeError",
        InvalidStateError: "InvalidStateError",
        SyntaxError: "SyntaxError",
        InvalidModificationError: "InvalidModificationError",
        NamespaceError: "NamespaceError",
        /**
         * @deprecated
         * Use TypeError for invalid arguments,
         * "NotSupportedError" DOMException for unsupported operations,
         * and "NotAllowedError" DOMException for denied requests instead.
         */
        InvalidAccessError: "InvalidAccessError",
        /**
         * @deprecated
         * Just to match the related static code, not part of the spec.
         */
        ValidationError: "ValidationError",
        /**
         * @deprecated
         * Use TypeError instead.
         */
        TypeMismatchError: "TypeMismatchError",
        SecurityError: "SecurityError",
        NetworkError: "NetworkError",
        AbortError: "AbortError",
        /**
         * @deprecated
         * Just to match the related static code, not part of the spec.
         */
        URLMismatchError: "URLMismatchError",
        QuotaExceededError: "QuotaExceededError",
        TimeoutError: "TimeoutError",
        InvalidNodeTypeError: "InvalidNodeTypeError",
        DataCloneError: "DataCloneError",
        EncodingError: "EncodingError",
        NotReadableError: "NotReadableError",
        UnknownError: "UnknownError",
        ConstraintError: "ConstraintError",
        DataError: "DataError",
        TransactionInactiveError: "TransactionInactiveError",
        ReadOnlyError: "ReadOnlyError",
        VersionError: "VersionError",
        OperationError: "OperationError",
        NotAllowedError: "NotAllowedError",
        OptOutError: "OptOutError"
      });
      var DOMExceptionNames = Object.keys(DOMExceptionName);
      function isValidDomExceptionCode(value) {
        return typeof value === "number" && value >= 1 && value <= 25;
      }
      function endsWithError(value) {
        return typeof value === "string" && value.substring(value.length - DOMExceptionName.Error.length) === DOMExceptionName.Error;
      }
      function DOMException2(messageOrCode, nameOrMessage) {
        if (isValidDomExceptionCode(messageOrCode)) {
          this.name = DOMExceptionNames[messageOrCode];
          this.message = nameOrMessage || "";
        } else {
          this.message = messageOrCode;
          this.name = endsWithError(nameOrMessage) ? nameOrMessage : DOMExceptionName.Error;
        }
        if (Error.captureStackTrace) Error.captureStackTrace(this, DOMException2);
      }
      extendError(DOMException2, true);
      Object.defineProperties(DOMException2.prototype, {
        code: {
          enumerable: true,
          get: function() {
            var code = DOMExceptionNames.indexOf(this.name);
            if (isValidDomExceptionCode(code)) return code;
            return 0;
          }
        }
      });
      var ExceptionCode = {
        INDEX_SIZE_ERR: 1,
        DOMSTRING_SIZE_ERR: 2,
        HIERARCHY_REQUEST_ERR: 3,
        WRONG_DOCUMENT_ERR: 4,
        INVALID_CHARACTER_ERR: 5,
        NO_DATA_ALLOWED_ERR: 6,
        NO_MODIFICATION_ALLOWED_ERR: 7,
        NOT_FOUND_ERR: 8,
        NOT_SUPPORTED_ERR: 9,
        INUSE_ATTRIBUTE_ERR: 10,
        INVALID_STATE_ERR: 11,
        SYNTAX_ERR: 12,
        INVALID_MODIFICATION_ERR: 13,
        NAMESPACE_ERR: 14,
        INVALID_ACCESS_ERR: 15,
        VALIDATION_ERR: 16,
        TYPE_MISMATCH_ERR: 17,
        SECURITY_ERR: 18,
        NETWORK_ERR: 19,
        ABORT_ERR: 20,
        URL_MISMATCH_ERR: 21,
        QUOTA_EXCEEDED_ERR: 22,
        TIMEOUT_ERR: 23,
        INVALID_NODE_TYPE_ERR: 24,
        DATA_CLONE_ERR: 25
      };
      var entries = Object.entries(ExceptionCode);
      for (i = 0; i < entries.length; i++) {
        key2 = entries[i][0];
        DOMException2[key2] = entries[i][1];
      }
      var key2;
      var i;
      function ParseError(message, locator, cause) {
        this.message = message;
        this.locator = locator;
        this.cause = cause;
        if (Error.captureStackTrace) Error.captureStackTrace(this, ParseError);
      }
      extendError(ParseError);
      exports.DOMException = DOMException2;
      exports.DOMExceptionName = DOMExceptionName;
      exports.ExceptionCode = ExceptionCode;
      exports.ParseError = ParseError;
    }
  });

  // node_modules/@xmldom/xmldom/lib/grammar.js
  var require_grammar = __commonJS({
    "node_modules/@xmldom/xmldom/lib/grammar.js"(exports) {
      "use strict";
      function detectUnicodeSupport(RegExpImpl) {
        try {
          if (typeof RegExpImpl !== "function") {
            RegExpImpl = RegExp;
          }
          var match = new RegExpImpl("\u{1D306}", "u").exec("\u{1D306}");
          return !!match && match[0].length === 2;
        } catch (error) {
        }
        return false;
      }
      var UNICODE_SUPPORT = detectUnicodeSupport();
      function chars(regexp) {
        if (regexp.source[0] !== "[") {
          throw new Error(regexp + " can not be used with chars");
        }
        return regexp.source.slice(1, regexp.source.lastIndexOf("]"));
      }
      function chars_without(regexp, search) {
        if (regexp.source[0] !== "[") {
          throw new Error("/" + regexp.source + "/ can not be used with chars_without");
        }
        if (!search || typeof search !== "string") {
          throw new Error(JSON.stringify(search) + " is not a valid search");
        }
        if (regexp.source.indexOf(search) === -1) {
          throw new Error('"' + search + '" is not is /' + regexp.source + "/");
        }
        if (search === "-" && regexp.source.indexOf(search) !== 1) {
          throw new Error('"' + search + '" is not at the first postion of /' + regexp.source + "/");
        }
        return new RegExp(regexp.source.replace(search, ""), UNICODE_SUPPORT ? "u" : "");
      }
      function reg(args) {
        var self = this;
        return new RegExp(
          Array.prototype.slice.call(arguments).map(function(part) {
            var isStr = typeof part === "string";
            if (isStr && self === void 0 && part === "|") {
              throw new Error("use regg instead of reg to wrap expressions with `|`!");
            }
            return isStr ? part : part.source;
          }).join(""),
          UNICODE_SUPPORT ? "u" : ""
        );
      }
      function regg(args) {
        if (arguments.length === 0) {
          throw new Error("no parameters provided");
        }
        return reg.apply(regg, ["(?:"].concat(Array.prototype.slice.call(arguments), [")"]));
      }
      var UNICODE_REPLACEMENT_CHARACTER = "\uFFFD";
      var Char = /[-\x09\x0A\x0D\x20-\x2C\x2E-\uD7FF\uE000-\uFFFD]/;
      if (UNICODE_SUPPORT) {
        Char = reg("[", chars(Char), "\\u{10000}-\\u{10FFFF}", "]");
      }
      var InvalidChar = new RegExp("[^" + chars(Char) + "]", UNICODE_SUPPORT ? "u" : "");
      var _SChar = /[\x20\x09\x0D\x0A]/;
      var SChar_s = chars(_SChar);
      var S = reg(_SChar, "+");
      var S_OPT = reg(_SChar, "*");
      var NameStartChar = /[:_a-zA-Z\xC0-\xD6\xD8-\xF6\xF8-\u02FF\u0370-\u1FFF\u200C-\u200D\u2070-\u218F\u2C00-\u2FEF\u3001-\uD7FF\uF900-\uFDCF\uFDF0-\uFFFD]/;
      if (UNICODE_SUPPORT) {
        NameStartChar = reg("[", chars(NameStartChar), "\\u{10000}-\\u{10FFFF}", "]");
      }
      var NameStartChar_s = chars(NameStartChar);
      var NameChar = reg("[", NameStartChar_s, chars(/[-.0-9\xB7]/), chars(/[\u0300-\u036F\u203F-\u2040]/), "]");
      var Name = reg(NameStartChar, NameChar, "*");
      var Name_exact = reg("^", Name, "$");
      var Nmtoken = reg(NameChar, "+");
      var EntityRef = reg("&", Name, ";");
      var CharRef = regg(/&#[0-9]+;|&#x[0-9a-fA-F]+;/);
      var Reference = regg(EntityRef, "|", CharRef);
      var PEReference = reg("%", Name, ";");
      var EntityValue = regg(
        reg('"', regg(/[^%&"]/, "|", PEReference, "|", Reference), "*", '"'),
        "|",
        reg("'", regg(/[^%&']/, "|", PEReference, "|", Reference), "*", "'")
      );
      var AttValue = regg('"', regg(/[^<&"]/, "|", Reference), "*", '"', "|", "'", regg(/[^<&']/, "|", Reference), "*", "'");
      var NCNameStartChar = chars_without(NameStartChar, ":");
      var NCNameChar = chars_without(NameChar, ":");
      var NCName = reg(NCNameStartChar, NCNameChar, "*");
      var NCName_exact = reg("^", NCName, "$");
      var QName = reg(NCName, regg(":", NCName), "?");
      var QName_exact = reg("^", QName, "$");
      var QName_group = reg("(", QName, ")");
      var SystemLiteral = regg(/"[^"]*"|'[^']*'/);
      var PI = reg(/^<\?/, "(", Name, ")", regg(S, "(?!", _SChar, ")(", Char, "*?)"), "?", /\?>/);
      var PubidChar = /[\x20\x0D\x0Aa-zA-Z0-9-'()+,./:=?;!*#@$_%]/;
      var PubidLiteral = regg('"', PubidChar, '*"', "|", "'", chars_without(PubidChar, "'"), "*'");
      var COMMENT_START = "<!--";
      var COMMENT_END = "-->";
      var Comment = reg(COMMENT_START, regg(chars_without(Char, "-"), "|", reg("-", chars_without(Char, "-"))), "*", COMMENT_END);
      var PCDATA = "#PCDATA";
      var Mixed = regg(
        reg(/\(/, S_OPT, PCDATA, regg(S_OPT, /\|/, S_OPT, QName), "*", S_OPT, /\)\*/),
        "|",
        reg(/\(/, S_OPT, PCDATA, S_OPT, /\)/)
      );
      var _children_quantity = /[?*+]?/;
      var children = reg(
        /\([^>]+\)/,
        _children_quantity
        /*regg(choice, '|', seq), _children_quantity*/
      );
      var contentspec = regg("EMPTY", "|", "ANY", "|", Mixed, "|", children);
      var ELEMENTDECL_START = "<!ELEMENT";
      var elementdecl = reg(ELEMENTDECL_START, S, regg(QName, "|", PEReference), S, regg(contentspec, "|", PEReference), S_OPT, ">");
      var NotationType = reg("NOTATION", S, /\(/, S_OPT, Name, regg(S_OPT, /\|/, S_OPT, Name), "*", S_OPT, /\)/);
      var Enumeration = reg(/\(/, S_OPT, Nmtoken, regg(S_OPT, /\|/, S_OPT, Nmtoken), "*", S_OPT, /\)/);
      var EnumeratedType = regg(NotationType, "|", Enumeration);
      var AttType = regg(/CDATA|ID|IDREF|IDREFS|ENTITY|ENTITIES|NMTOKEN|NMTOKENS/, "|", EnumeratedType);
      var DefaultDecl = regg(/#REQUIRED|#IMPLIED/, "|", regg(regg("#FIXED", S), "?", AttValue));
      var AttDef = regg(S, Name, S, AttType, S, DefaultDecl);
      var ATTLIST_DECL_START = "<!ATTLIST";
      var AttlistDecl = reg(ATTLIST_DECL_START, S, Name, AttDef, "*", S_OPT, ">");
      var ABOUT_LEGACY_COMPAT = "about:legacy-compat";
      var ABOUT_LEGACY_COMPAT_SystemLiteral = regg('"' + ABOUT_LEGACY_COMPAT + '"', "|", "'" + ABOUT_LEGACY_COMPAT + "'");
      var SYSTEM = "SYSTEM";
      var PUBLIC = "PUBLIC";
      var ExternalID = regg(regg(SYSTEM, S, SystemLiteral), "|", regg(PUBLIC, S, PubidLiteral, S, SystemLiteral));
      var ExternalID_match = reg(
        "^",
        regg(
          regg(SYSTEM, S, "(?<SystemLiteralOnly>", SystemLiteral, ")"),
          "|",
          regg(PUBLIC, S, "(?<PubidLiteral>", PubidLiteral, ")", S, "(?<SystemLiteral>", SystemLiteral, ")")
        )
      );
      var PubidLiteral_match = reg("^", PubidLiteral, "$");
      var SystemLiteral_match = reg("^", SystemLiteral, "$");
      var NDataDecl = regg(S, "NDATA", S, Name);
      var EntityDef = regg(EntityValue, "|", regg(ExternalID, NDataDecl, "?"));
      var ENTITY_DECL_START = "<!ENTITY";
      var GEDecl = reg(ENTITY_DECL_START, S, Name, S, EntityDef, S_OPT, ">");
      var PEDef = regg(EntityValue, "|", ExternalID);
      var PEDecl = reg(ENTITY_DECL_START, S, "%", S, Name, S, PEDef, S_OPT, ">");
      var EntityDecl = regg(GEDecl, "|", PEDecl);
      var PublicID = reg(PUBLIC, S, PubidLiteral);
      var NotationDecl = reg("<!NOTATION", S, Name, S, regg(ExternalID, "|", PublicID), S_OPT, ">");
      var Eq = reg(S_OPT, "=", S_OPT);
      var VersionNum = /1[.]\d+/;
      var VersionInfo = reg(S, "version", Eq, regg("'", VersionNum, "'", "|", '"', VersionNum, '"'));
      var EncName = /[A-Za-z][-A-Za-z0-9._]*/;
      var EncodingDecl = regg(S, "encoding", Eq, regg('"', EncName, '"', "|", "'", EncName, "'"));
      var SDDecl = regg(S, "standalone", Eq, regg("'", regg("yes", "|", "no"), "'", "|", '"', regg("yes", "|", "no"), '"'));
      var XMLDecl = reg(/^<\?xml/, VersionInfo, EncodingDecl, "?", SDDecl, "?", S_OPT, /\?>/);
      var DOCTYPE_DECL_START = "<!DOCTYPE";
      var CDATA_START = "<![CDATA[";
      var CDATA_END = "]]>";
      var CDStart = /<!\[CDATA\[/;
      var CDEnd = /\]\]>/;
      var CData = reg(Char, "*?", CDEnd);
      var CDSect = reg(CDStart, CData);
      exports.chars = chars;
      exports.chars_without = chars_without;
      exports.detectUnicodeSupport = detectUnicodeSupport;
      exports.reg = reg;
      exports.regg = regg;
      exports.ABOUT_LEGACY_COMPAT = ABOUT_LEGACY_COMPAT;
      exports.ABOUT_LEGACY_COMPAT_SystemLiteral = ABOUT_LEGACY_COMPAT_SystemLiteral;
      exports.AttlistDecl = AttlistDecl;
      exports.CDATA_START = CDATA_START;
      exports.CDATA_END = CDATA_END;
      exports.CDSect = CDSect;
      exports.Char = Char;
      exports.Comment = Comment;
      exports.COMMENT_START = COMMENT_START;
      exports.COMMENT_END = COMMENT_END;
      exports.DOCTYPE_DECL_START = DOCTYPE_DECL_START;
      exports.elementdecl = elementdecl;
      exports.EntityDecl = EntityDecl;
      exports.EntityValue = EntityValue;
      exports.ExternalID = ExternalID;
      exports.ExternalID_match = ExternalID_match;
      exports.Name = Name;
      exports.Name_exact = Name_exact;
      exports.NCName_exact = NCName_exact;
      exports.NotationDecl = NotationDecl;
      exports.Reference = Reference;
      exports.PEReference = PEReference;
      exports.PI = PI;
      exports.PUBLIC = PUBLIC;
      exports.PubidLiteral = PubidLiteral;
      exports.PubidLiteral_match = PubidLiteral_match;
      exports.QName = QName;
      exports.QName_exact = QName_exact;
      exports.QName_group = QName_group;
      exports.S = S;
      exports.SChar_s = SChar_s;
      exports.S_OPT = S_OPT;
      exports.SYSTEM = SYSTEM;
      exports.SystemLiteral = SystemLiteral;
      exports.SystemLiteral_match = SystemLiteral_match;
      exports.InvalidChar = InvalidChar;
      exports.UNICODE_REPLACEMENT_CHARACTER = UNICODE_REPLACEMENT_CHARACTER;
      exports.UNICODE_SUPPORT = UNICODE_SUPPORT;
      exports.XMLDecl = XMLDecl;
    }
  });

  // node_modules/@xmldom/xmldom/lib/dom.js
  var require_dom = __commonJS({
    "node_modules/@xmldom/xmldom/lib/dom.js"(exports) {
      "use strict";
      var conventions = require_conventions();
      var find = conventions.find;
      var hasDefaultHTMLNamespace = conventions.hasDefaultHTMLNamespace;
      var hasOwn = conventions.hasOwn;
      var isHTMLMimeType = conventions.isHTMLMimeType;
      var isHTMLRawTextElement = conventions.isHTMLRawTextElement;
      var isHTMLVoidElement = conventions.isHTMLVoidElement;
      var MIME_TYPE = conventions.MIME_TYPE;
      var NAMESPACE = conventions.NAMESPACE;
      var PDC = Symbol();
      var errors = require_errors();
      var DOMException2 = errors.DOMException;
      var DOMExceptionName = errors.DOMExceptionName;
      var g = require_grammar();
      function checkSymbol(symbol) {
        if (symbol !== PDC) {
          throw new TypeError("Illegal constructor");
        }
      }
      function notEmptyString(input) {
        return input !== "";
      }
      function splitOnASCIIWhitespace(input) {
        return input ? input.split(/[\t\n\f\r ]+/).filter(notEmptyString) : [];
      }
      function orderedSetReducer(current, element) {
        if (!hasOwn(current, element)) {
          current[element] = true;
        }
        return current;
      }
      function toOrderedSet(input) {
        if (!input) return [];
        var list = splitOnASCIIWhitespace(input);
        return Object.keys(list.reduce(orderedSetReducer, {}));
      }
      function arrayIncludes(list) {
        return function(element) {
          return list && list.indexOf(element) !== -1;
        };
      }
      function validateQualifiedName(qualifiedName) {
        if (!g.QName_exact.test(qualifiedName)) {
          throw new DOMException2(DOMException2.INVALID_CHARACTER_ERR, 'invalid character in qualified name "' + qualifiedName + '"');
        }
      }
      function validateAndExtract(namespace, qualifiedName) {
        validateQualifiedName(qualifiedName);
        namespace = namespace || null;
        var prefix = null;
        var localName = qualifiedName;
        if (qualifiedName.indexOf(":") >= 0) {
          var splitResult = qualifiedName.split(":");
          prefix = splitResult[0];
          localName = splitResult[1];
        }
        if (prefix !== null && namespace === null) {
          throw new DOMException2(DOMException2.NAMESPACE_ERR, "prefix is non-null and namespace is null");
        }
        if (prefix === "xml" && namespace !== conventions.NAMESPACE.XML) {
          throw new DOMException2(DOMException2.NAMESPACE_ERR, 'prefix is "xml" and namespace is not the XML namespace');
        }
        if ((prefix === "xmlns" || qualifiedName === "xmlns") && namespace !== conventions.NAMESPACE.XMLNS) {
          throw new DOMException2(
            DOMException2.NAMESPACE_ERR,
            'either qualifiedName or prefix is "xmlns" and namespace is not the XMLNS namespace'
          );
        }
        if (namespace === conventions.NAMESPACE.XMLNS && prefix !== "xmlns" && qualifiedName !== "xmlns") {
          throw new DOMException2(
            DOMException2.NAMESPACE_ERR,
            'namespace is the XMLNS namespace and neither qualifiedName nor prefix is "xmlns"'
          );
        }
        return [namespace, prefix, localName];
      }
      function copy(src, dest) {
        for (var p in src) {
          if (hasOwn(src, p)) {
            dest[p] = src[p];
          }
        }
      }
      function _extends(Class, Super) {
        var pt = Class.prototype;
        if (!(pt instanceof Super)) {
          let t = function() {
          };
          t.prototype = Super.prototype;
          t = new t();
          copy(pt, t);
          Class.prototype = pt = t;
        }
        if (pt.constructor != Class) {
          if (typeof Class != "function") {
            console.error("unknown Class:" + Class);
          }
          pt.constructor = Class;
        }
      }
      var NodeType = {};
      var ELEMENT_NODE = NodeType.ELEMENT_NODE = 1;
      var ATTRIBUTE_NODE = NodeType.ATTRIBUTE_NODE = 2;
      var TEXT_NODE = NodeType.TEXT_NODE = 3;
      var CDATA_SECTION_NODE = NodeType.CDATA_SECTION_NODE = 4;
      var ENTITY_REFERENCE_NODE = NodeType.ENTITY_REFERENCE_NODE = 5;
      var ENTITY_NODE = NodeType.ENTITY_NODE = 6;
      var PROCESSING_INSTRUCTION_NODE = NodeType.PROCESSING_INSTRUCTION_NODE = 7;
      var COMMENT_NODE = NodeType.COMMENT_NODE = 8;
      var DOCUMENT_NODE = NodeType.DOCUMENT_NODE = 9;
      var DOCUMENT_TYPE_NODE = NodeType.DOCUMENT_TYPE_NODE = 10;
      var DOCUMENT_FRAGMENT_NODE = NodeType.DOCUMENT_FRAGMENT_NODE = 11;
      var NOTATION_NODE = NodeType.NOTATION_NODE = 12;
      var DocumentPosition = conventions.freeze({
        DOCUMENT_POSITION_DISCONNECTED: 1,
        DOCUMENT_POSITION_PRECEDING: 2,
        DOCUMENT_POSITION_FOLLOWING: 4,
        DOCUMENT_POSITION_CONTAINS: 8,
        DOCUMENT_POSITION_CONTAINED_BY: 16,
        DOCUMENT_POSITION_IMPLEMENTATION_SPECIFIC: 32
      });
      function commonAncestor(a, b) {
        if (b.length < a.length) return commonAncestor(b, a);
        var c = null;
        for (var n in a) {
          if (a[n] !== b[n]) return c;
          c = a[n];
        }
        return c;
      }
      function docGUID(doc) {
        if (!doc.guid) doc.guid = Math.random();
        return doc.guid;
      }
      function NodeList() {
      }
      NodeList.prototype = {
        /**
         * The number of nodes in the list. The range of valid child node indices is 0 to length-1
         * inclusive.
         *
         * @type {number}
         */
        length: 0,
        /**
         * Returns the item at `index`. If index is greater than or equal to the number of nodes in
         * the list, this returns null.
         *
         * @param index
         * Unsigned long Index into the collection.
         * @returns {Node | null}
         * The node at position `index` in the NodeList,
         * or null if that is not a valid index.
         */
        item: function(index) {
          return index >= 0 && index < this.length ? this[index] : null;
        },
        /**
         * Returns a string representation of the NodeList.
         *
         * Accepts the same `options` object as `XMLSerializer.prototype.serializeToString`
         * (`requireWellFormed`, `splitCDATASections`, `nodeFilter`). Passing a function is treated as
         * a legacy `nodeFilter` for backward compatibility.
         *
         * @param {Object | function} [options]
         * @param {boolean} [options.requireWellFormed=false]
         * @param {boolean} [options.splitCDATASections=true]
         * @param {function} [options.nodeFilter]
         * @returns {string}
         */
        toString: function(options) {
          var opts;
          if (typeof options === "function") {
            opts = { requireWellFormed: false, splitCDATASections: true, nodeFilter: options };
          } else if (!!options) {
            opts = {
              requireWellFormed: !!options.requireWellFormed,
              splitCDATASections: options.splitCDATASections !== false,
              nodeFilter: options.nodeFilter || null
            };
          } else {
            opts = { requireWellFormed: false, splitCDATASections: true, nodeFilter: null };
          }
          for (var buf = [], i = 0; i < this.length; i++) {
            serializeToString(this[i], buf, null, opts);
          }
          return buf.join("");
        },
        /**
         * Filters the NodeList based on a predicate.
         *
         * @param {function(Node): boolean} predicate
         * - A predicate function to filter the NodeList.
         * @returns {Node[]}
         * An array of nodes that satisfy the predicate.
         * @private
         */
        filter: function(predicate) {
          return Array.prototype.filter.call(this, predicate);
        },
        /**
         * Returns the first index at which a given node can be found in the NodeList, or -1 if it is
         * not present.
         *
         * @param {Node} item
         * - The Node item to locate in the NodeList.
         * @returns {number}
         * The first index of the node in the NodeList; -1 if not found.
         * @private
         */
        indexOf: function(item) {
          return Array.prototype.indexOf.call(this, item);
        }
      };
      NodeList.prototype[Symbol.iterator] = function() {
        var me = this;
        var index = 0;
        return {
          next: function() {
            if (index < me.length) {
              return {
                value: me[index++],
                done: false
              };
            } else {
              return {
                done: true
              };
            }
          },
          return: function() {
            return {
              done: true
            };
          }
        };
      };
      function LiveNodeList(node, refresh) {
        this._node = node;
        this._refresh = refresh;
        _updateLiveList(this);
      }
      function _updateLiveList(list) {
        var inc = list._node._inc || list._node.ownerDocument._inc;
        if (list._inc !== inc) {
          var ls = list._refresh(list._node);
          __set__(list, "length", ls.length);
          if (!list.$$length || ls.length < list.$$length) {
            for (var i = ls.length; i in list; i++) {
              if (hasOwn(list, i)) {
                delete list[i];
              }
            }
          }
          copy(ls, list);
          list._inc = inc;
        }
      }
      LiveNodeList.prototype.item = function(i) {
        _updateLiveList(this);
        return this[i] || null;
      };
      _extends(LiveNodeList, NodeList);
      function NamedNodeMap() {
        this._nsIndex = /* @__PURE__ */ Object.create(null);
        this._noNsIndex = /* @__PURE__ */ Object.create(null);
      }
      function _findNodeIndex(list, node) {
        var i = 0;
        while (i < list.length) {
          if (list[i] === node) {
            return i;
          }
          i++;
        }
      }
      function _nnmBucket(map, namespaceURI, create) {
        if (!namespaceURI) {
          return map._noNsIndex;
        }
        var bucket = map._nsIndex[namespaceURI];
        if (!bucket && create) {
          bucket = map._nsIndex[namespaceURI] = /* @__PURE__ */ Object.create(null);
        }
        return bucket;
      }
      function _nnmIndexFind(map, namespaceURI, localName) {
        var bucket = _nnmBucket(map, namespaceURI, false);
        var found = bucket && bucket[localName];
        return found ? found : null;
      }
      function _nnmIndexAdd(map, attr) {
        _nnmBucket(map, attr.namespaceURI, true)[attr.localName] = attr;
      }
      function _nnmIndexRemove(map, attr) {
        var bucket = _nnmBucket(map, attr.namespaceURI, false);
        if (bucket) {
          delete bucket[attr.localName];
        }
      }
      function _addNamedNode(el, list, newAttr, oldAttr) {
        if (oldAttr) {
          list[_findNodeIndex(list, oldAttr)] = newAttr;
        } else {
          list[list.length] = newAttr;
          list.length++;
        }
        _nnmIndexAdd(list, newAttr);
        if (el) {
          newAttr.ownerElement = el;
          var doc = el.ownerDocument;
          if (doc) {
            oldAttr && _onRemoveAttribute(doc, el, oldAttr);
            _onAddAttribute(doc, el, newAttr);
          }
        }
      }
      function _removeNamedNode(el, list, attr) {
        var i = _findNodeIndex(list, attr);
        if (i >= 0) {
          var lastIndex = list.length - 1;
          while (i <= lastIndex) {
            list[i] = list[++i];
          }
          list.length = lastIndex;
          _nnmIndexRemove(list, attr);
          if (el) {
            var doc = el.ownerDocument;
            if (doc) {
              _onRemoveAttribute(doc, el, attr);
            }
            attr.ownerElement = null;
          }
        }
      }
      NamedNodeMap.prototype = {
        length: 0,
        item: NodeList.prototype.item,
        /**
         * Get an attribute by name. Note: Name is in lower case in case of HTML namespace and
         * document.
         *
         * @param {string} localName
         * The local name of the attribute.
         * @returns {Attr | null}
         * The attribute with the given local name, or null if no such attribute exists.
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-get-by-name
         */
        getNamedItem: function(localName) {
          if (this._ownerElement && this._ownerElement._isInHTMLDocumentAndNamespace()) {
            localName = localName.toLowerCase();
          }
          var i = 0;
          while (i < this.length) {
            var attr = this[i];
            if (attr.nodeName === localName) {
              return attr;
            }
            i++;
          }
          return null;
        },
        /**
         * Set an attribute.
         *
         * @param {Attr} attr
         * The attribute to set.
         * @returns {Attr | null}
         * The old attribute with the same local name and namespace URI as the new one, or null if no
         * such attribute exists.
         * @throws {DOMException}
         * With code:
         * - {@link INUSE_ATTRIBUTE_ERR} - If the attribute is already an attribute of another
         * element.
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-set
         */
        setNamedItem: function(attr) {
          var el = attr.ownerElement;
          if (el && el !== this._ownerElement) {
            throw new DOMException2(DOMException2.INUSE_ATTRIBUTE_ERR);
          }
          var oldAttr = _nnmIndexFind(this, attr.namespaceURI, attr.localName);
          if (oldAttr === attr) {
            return attr;
          }
          _addNamedNode(this._ownerElement, this, attr, oldAttr);
          return oldAttr;
        },
        /**
         * Set an attribute, replacing an existing attribute with the same local name and namespace
         * URI if one exists.
         *
         * @param {Attr} attr
         * The attribute to set.
         * @returns {Attr | null}
         * The old attribute with the same local name and namespace URI as the new one, or null if no
         * such attribute exists.
         * @throws {DOMException}
         * Throws a DOMException with the name "InUseAttributeError" if the attribute is already an
         * attribute of another element.
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-set
         */
        setNamedItemNS: function(attr) {
          return this.setNamedItem(attr);
        },
        /**
         * Removes an attribute specified by the local name.
         *
         * @param {string} localName
         * The local name of the attribute to be removed.
         * @returns {Attr}
         * The attribute node that was removed.
         * @throws {DOMException}
         * With code:
         * - {@link DOMException.NOT_FOUND_ERR} if no attribute with the given name is found.
         * @see https://dom.spec.whatwg.org/#dom-namednodemap-removenameditem
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-remove-by-name
         */
        removeNamedItem: function(localName) {
          var attr = this.getNamedItem(localName);
          if (!attr) {
            throw new DOMException2(DOMException2.NOT_FOUND_ERR, localName);
          }
          _removeNamedNode(this._ownerElement, this, attr);
          return attr;
        },
        /**
         * Removes an attribute specified by the namespace and local name.
         *
         * @param {string | null} namespaceURI
         * The namespace URI of the attribute to be removed.
         * @param {string} localName
         * The local name of the attribute to be removed.
         * @returns {Attr}
         * The attribute node that was removed.
         * @throws {DOMException}
         * With code:
         * - {@link DOMException.NOT_FOUND_ERR} if no attribute with the given namespace URI and local
         * name is found.
         * @see https://dom.spec.whatwg.org/#dom-namednodemap-removenameditemns
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-remove-by-namespace
         */
        removeNamedItemNS: function(namespaceURI, localName) {
          var attr = this.getNamedItemNS(namespaceURI, localName);
          if (!attr) {
            throw new DOMException2(DOMException2.NOT_FOUND_ERR, namespaceURI ? namespaceURI + " : " + localName : localName);
          }
          _removeNamedNode(this._ownerElement, this, attr);
          return attr;
        },
        /**
         * Get an attribute by namespace and local name.
         *
         * @param {string | null} namespaceURI
         * The namespace URI of the attribute.
         * @param {string} localName
         * The local name of the attribute.
         * @returns {Attr | null}
         * The attribute with the given namespace URI and local name, or null if no such attribute
         * exists.
         * @see https://dom.spec.whatwg.org/#concept-element-attributes-get-by-namespace
         */
        getNamedItemNS: function(namespaceURI, localName) {
          if (!namespaceURI) {
            namespaceURI = null;
          }
          var i = 0;
          while (i < this.length) {
            var node = this[i];
            if (node.localName === localName && node.namespaceURI === namespaceURI) {
              return node;
            }
            i++;
          }
          return null;
        }
      };
      NamedNodeMap.prototype[Symbol.iterator] = function() {
        var me = this;
        var index = 0;
        return {
          next: function() {
            if (index < me.length) {
              return {
                value: me[index++],
                done: false
              };
            } else {
              return {
                done: true
              };
            }
          },
          return: function() {
            return {
              done: true
            };
          }
        };
      };
      function DOMImplementation() {
      }
      DOMImplementation.prototype = {
        /**
         * Test if the DOM implementation implements a specific feature and version, as specified in
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/core.html#DOMFeatures DOM Features}.
         *
         * The DOMImplementation.hasFeature() method returns a Boolean flag indicating if a given
         * feature is supported. The different implementations fairly diverged in what kind of
         * features were reported. The latest version of the spec settled to force this method to
         * always return true, where the functionality was accurate and in use.
         *
         * @deprecated
         * It is deprecated and modern browsers return true in all cases.
         * @function DOMImplementation#hasFeature
         * @param {string} feature
         * The name of the feature to test.
         * @param {string} [version]
         * This is the version number of the feature to test.
         * @returns {boolean}
         * Always returns true.
         * @see https://developer.mozilla.org/en-US/docs/Web/API/DOMImplementation/hasFeature MDN
         * @see https://www.w3.org/TR/REC-DOM-Level-1/level-one-core.html#ID-5CED94D7 DOM Level 1 Core
         * @see https://dom.spec.whatwg.org/#dom-domimplementation-hasfeature DOM Living Standard
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#ID-5CED94D7 DOM Level 3 Core
         */
        hasFeature: function(feature, version) {
          return true;
        },
        /**
         * Creates a DOM Document object of the specified type with its document element. Note that
         * based on the {@link DocumentType}
         * given to create the document, the implementation may instantiate specialized
         * {@link Document} objects that support additional features than the "Core", such as "HTML"
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#DOM2HTML DOM Level 2 HTML}.
         * On the other hand, setting the {@link DocumentType} after the document was created makes
         * this very unlikely to happen. Alternatively, specialized {@link Document} creation methods,
         * such as createHTMLDocument
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#DOM2HTML DOM Level 2 HTML},
         * can be used to obtain specific types of {@link Document} objects.
         *
         * __It behaves slightly different from the description in the living standard__:
         * - There is no interface/class `XMLDocument`, it returns a `Document`
         * instance (with it's `type` set to `'xml'`).
         * - `encoding`, `mode`, `origin`, `url` fields are currently not declared.
         *
         * @function DOMImplementation.createDocument
         * @param {string | null} namespaceURI
         * The
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/glossary.html#dt-namespaceURI namespace URI}
         * of the document element to create or null.
         * @param {string | null} qualifiedName
         * The
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/glossary.html#dt-qualifiedname qualified name}
         * of the document element to be created or null.
         * @param {DocumentType | null} [doctype=null]
         * The type of document to be created or null. When doctype is not null, its
         * {@link Node#ownerDocument} attribute is set to the document being created. Default is
         * `null`
         * @returns {Document}
         * A new {@link Document} object with its document element. If the NamespaceURI,
         * qualifiedName, and doctype are null, the returned {@link Document} is empty with no
         * document element.
         * @throws {DOMException}
         * With code:
         *
         * - `INVALID_CHARACTER_ERR`: Raised if the specified qualified name is not an XML name
         * according to {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#XML XML 1.0}.
         * - `NAMESPACE_ERR`: Raised if the qualifiedName is malformed, if the qualifiedName has a
         * prefix and the namespaceURI is null, or if the qualifiedName is null and the namespaceURI
         * is different from null, or if the qualifiedName has a prefix that is "xml" and the
         * namespaceURI is different from "{@link http://www.w3.org/XML/1998/namespace}"
         * {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#Namespaces XML Namespaces},
         * or if the DOM implementation does not support the "XML" feature but a non-null namespace
         * URI was provided, since namespaces were defined by XML.
         * - `WRONG_DOCUMENT_ERR`: Raised if doctype has already been used with a different document
         * or was created from a different implementation.
         * - `NOT_SUPPORTED_ERR`: May be raised if the implementation does not support the feature
         * "XML" and the language exposed through the Document does not support XML Namespaces (such
         * as {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#HTML40 HTML 4.01}).
         * @since DOM Level 2.
         * @see {@link #createHTMLDocument}
         * @see https://developer.mozilla.org/en-US/docs/Web/API/DOMImplementation/createDocument MDN
         * @see https://dom.spec.whatwg.org/#dom-domimplementation-createdocument DOM Living Standard
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#Level-2-Core-DOM-createDocument DOM
         *      Level 3 Core
         * @see https://www.w3.org/TR/DOM-Level-2-Core/core.html#Level-2-Core-DOM-createDocument DOM
         *      Level 2 Core (initial)
         */
        createDocument: function(namespaceURI, qualifiedName, doctype) {
          var contentType = MIME_TYPE.XML_APPLICATION;
          if (namespaceURI === NAMESPACE.HTML) {
            contentType = MIME_TYPE.XML_XHTML_APPLICATION;
          } else if (namespaceURI === NAMESPACE.SVG) {
            contentType = MIME_TYPE.XML_SVG_IMAGE;
          }
          var doc = new Document(PDC, { contentType });
          doc.implementation = this;
          doc.childNodes = new NodeList();
          doc.doctype = doctype || null;
          if (doctype) {
            doc.appendChild(doctype);
          }
          if (qualifiedName) {
            var root = doc.createElementNS(namespaceURI, qualifiedName);
            doc.appendChild(root);
          }
          return doc;
        },
        /**
         * Creates an empty DocumentType node. Entity declarations and notations are not made
         * available. Entity reference expansions and default attribute additions do not occur.
         *
         * **This behavior is slightly different from the one in the specs**:
         * - `encoding`, `mode`, `origin`, `url` fields are currently not declared.
         * - `publicId` and `systemId` contain the raw data including any possible quotes,
         *   so they can always be serialized back to the original value
         * - `internalSubset` contains the raw string between `[` and `]` if present,
         *   but is not parsed or validated in any form.
         *
         * @function DOMImplementation#createDocumentType
         * @param {string} qualifiedName
         * The {@link https://www.w3.org/TR/DOM-Level-3-Core/glossary.html#dt-qualifiedname qualified
         * name} of the document type to be created.
         * @param {string} [publicId]
         * The external subset public identifier. Stored verbatim including surrounding quotes.
         * When serialized with `requireWellFormed: true`, the serializer throws `InvalidStateError`
         * if the value is non-empty and does not match the XML `PubidLiteral` production
         * (W3C DOM Parsing §3.2.1.3; XML 1.0 production [12]). Creation-time validation is not
         * enforced — deferred to a future breaking release.
         * @param {string} [systemId]
         * The external subset system identifier. Stored verbatim including surrounding quotes.
         * When serialized with `requireWellFormed: true`, the serializer throws `InvalidStateError`
         * if the value is non-empty and does not match the XML `SystemLiteral` production
         * (W3C DOM Parsing §3.2.1.3; XML 1.0 production [11]). Creation-time validation is not
         * enforced — deferred to a future breaking release.
         * @param {string} [internalSubset]
         * The internal subset or an empty string if it is not present. Stored verbatim.
         * When serialized with `requireWellFormed: true`, the serializer throws `InvalidStateError`
         * if the value contains `"]>"`. Creation-time validation is not enforced.
         * @returns {DocumentType}
         * A new {@link DocumentType} node with {@link Node#ownerDocument} set to null.
         * @throws {DOMException}
         * With code:
         *
         * - `INVALID_CHARACTER_ERR`: Raised if the specified qualified name is not an XML name
         * according to {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#XML XML 1.0}.
         * - `NAMESPACE_ERR`: Raised if the qualifiedName is malformed.
         * - `NOT_SUPPORTED_ERR`: May be raised if the implementation does not support the feature
         * "XML" and the language exposed through the Document does not support XML Namespaces (such
         * as {@link https://www.w3.org/TR/DOM-Level-3-Core/references.html#HTML40 HTML 4.01}).
         * @since DOM Level 2.
         * @see https://developer.mozilla.org/en-US/docs/Web/API/DOMImplementation/createDocumentType
         *      MDN
         * @see https://dom.spec.whatwg.org/#dom-domimplementation-createdocumenttype DOM Living
         *      Standard
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#Level-3-Core-DOM-createDocType DOM
         *      Level 3 Core
         * @see https://www.w3.org/TR/DOM-Level-2-Core/core.html#Level-2-Core-DOM-createDocType DOM
         *      Level 2 Core
         * @see https://github.com/xmldom/xmldom/blob/master/CHANGELOG.md#050
         * @see https://www.w3.org/TR/DOM-Level-2-Core/#core-ID-Core-DocType-internalSubset
         * @prettierignore
         */
        createDocumentType: function(qualifiedName, publicId, systemId, internalSubset) {
          validateQualifiedName(qualifiedName);
          var node = new DocumentType(PDC);
          node.name = qualifiedName;
          node.nodeName = qualifiedName;
          node.publicId = publicId || "";
          node.systemId = systemId || "";
          node.internalSubset = internalSubset || "";
          node.childNodes = new NodeList();
          return node;
        },
        /**
         * Returns an HTML document, that might already have a basic DOM structure.
         *
         * __It behaves slightly different from the description in the living standard__:
         * - If the first argument is `false` no initial nodes are added (steps 3-7 in the specs are
         * omitted)
         * - `encoding`, `mode`, `origin`, `url` fields are currently not declared.
         *
         * @param {string | false} [title]
         * A string containing the title to give the new HTML document.
         * @returns {Document}
         * The HTML document.
         * @since WHATWG Living Standard.
         * @see {@link #createDocument}
         * @see https://dom.spec.whatwg.org/#dom-domimplementation-createhtmldocument
         * @see https://dom.spec.whatwg.org/#html-document
         */
        createHTMLDocument: function(title) {
          var doc = new Document(PDC, { contentType: MIME_TYPE.HTML });
          doc.implementation = this;
          doc.childNodes = new NodeList();
          if (title !== false) {
            doc.doctype = this.createDocumentType("html");
            doc.doctype.ownerDocument = doc;
            doc.appendChild(doc.doctype);
            var htmlNode = doc.createElement("html");
            doc.appendChild(htmlNode);
            var headNode = doc.createElement("head");
            htmlNode.appendChild(headNode);
            if (typeof title === "string") {
              var titleNode = doc.createElement("title");
              titleNode.appendChild(doc.createTextNode(title));
              headNode.appendChild(titleNode);
            }
            htmlNode.appendChild(doc.createElement("body"));
          }
          return doc;
        }
      };
      function Node(symbol) {
        checkSymbol(symbol);
      }
      Node.prototype = {
        /**
         * The first child of this node.
         *
         * @type {Node | null}
         */
        firstChild: null,
        /**
         * The last child of this node.
         *
         * @type {Node | null}
         */
        lastChild: null,
        /**
         * The previous sibling of this node.
         *
         * @type {Node | null}
         */
        previousSibling: null,
        /**
         * The next sibling of this node.
         *
         * @type {Node | null}
         */
        nextSibling: null,
        /**
         * The parent node of this node.
         *
         * @type {Node | null}
         */
        parentNode: null,
        /**
         * The parent element of this node.
         *
         * @type {Element | null}
         */
        get parentElement() {
          return this.parentNode && this.parentNode.nodeType === this.ELEMENT_NODE ? this.parentNode : null;
        },
        /**
         * The child nodes of this node.
         *
         * @type {NodeList}
         */
        childNodes: null,
        /**
         * The document object associated with this node.
         *
         * @type {Document | null}
         */
        ownerDocument: null,
        /**
         * The value of this node.
         *
         * @type {string | null}
         */
        nodeValue: null,
        /**
         * The namespace URI of this node.
         *
         * @type {string | null}
         */
        namespaceURI: null,
        /**
         * The prefix of the namespace for this node.
         *
         * @type {string | null}
         */
        prefix: null,
        /**
         * The local part of the qualified name of this node.
         *
         * @type {string | null}
         */
        localName: null,
        /**
         * The baseURI is currently always `about:blank`,
         * since that's what happens when you create a document from scratch.
         *
         * @type {'about:blank'}
         */
        baseURI: "about:blank",
        /**
         * Is true if this node is part of a document.
         *
         * @type {boolean}
         */
        get isConnected() {
          var rootNode = this.getRootNode();
          return rootNode && rootNode.nodeType === rootNode.DOCUMENT_NODE;
        },
        /**
         * Checks whether `other` is an inclusive descendant of this node.
         *
         * @param {Node | null | undefined} other
         * The node to check.
         * @returns {boolean}
         * True if `other` is an inclusive descendant of this node; false otherwise.
         * @see https://dom.spec.whatwg.org/#dom-node-contains
         */
        contains: function(other) {
          if (!other) return false;
          var parent = other;
          do {
            if (this === parent) return true;
            parent = parent.parentNode;
          } while (parent);
          return false;
        },
        /**
         * @typedef GetRootNodeOptions
         * @property {boolean} [composed=false]
         */
        /**
         * Searches for the root node of this node.
         *
         * **This behavior is slightly different from the in the specs**:
         * - ignores `options.composed`, since `ShadowRoot`s are unsupported, always returns root.
         *
         * @param {GetRootNodeOptions} [options]
         * @returns {Node}
         * Root node.
         * @see https://dom.spec.whatwg.org/#dom-node-getrootnode
         * @see https://dom.spec.whatwg.org/#concept-shadow-including-root
         */
        getRootNode: function(options) {
          var parent = this;
          do {
            if (!parent.parentNode) {
              return parent;
            }
            parent = parent.parentNode;
          } while (parent);
        },
        /**
         * Checks whether the given node is equal to this node.
         *
         * Two nodes are equal when they have the same type, defining characteristics (for the type),
         * and the same childNodes. The comparison is iterative to avoid stack overflows on
         * deeply-nested trees. Attribute nodes of each Element pair are also pushed onto the stack
         * and compared the same way.
         *
         * @param {Node} [otherNode]
         * @returns {boolean}
         * @see https://dom.spec.whatwg.org/#concept-node-equals
         * @see ../docs/walk-dom.md.
         */
        isEqualNode: function(otherNode) {
          if (!otherNode) return false;
          var stack = [{ node: this, other: otherNode }];
          while (stack.length > 0) {
            var pair = stack.pop();
            var node = pair.node;
            var other = pair.other;
            if (node.nodeType !== other.nodeType) return false;
            switch (node.nodeType) {
              case node.DOCUMENT_TYPE_NODE:
                if (node.name !== other.name) return false;
                if (node.publicId !== other.publicId) return false;
                if (node.systemId !== other.systemId) return false;
                break;
              case node.ELEMENT_NODE:
                if (node.namespaceURI !== other.namespaceURI) return false;
                if (node.prefix !== other.prefix) return false;
                if (node.localName !== other.localName) return false;
                if (node.attributes.length !== other.attributes.length) return false;
                for (var i = 0; i < node.attributes.length; i++) {
                  var attr = node.attributes.item(i);
                  var otherAttr = other.getAttributeNodeNS(attr.namespaceURI, attr.localName);
                  if (!otherAttr) return false;
                  stack.push({ node: attr, other: otherAttr });
                }
                break;
              case node.ATTRIBUTE_NODE:
                if (node.namespaceURI !== other.namespaceURI) return false;
                if (node.localName !== other.localName) return false;
                if (node.value !== other.value) return false;
                break;
              case node.PROCESSING_INSTRUCTION_NODE:
                if (node.target !== other.target || node.data !== other.data) return false;
                break;
              case node.TEXT_NODE:
              case node.CDATA_SECTION_NODE:
              case node.COMMENT_NODE:
                if (node.data !== other.data) return false;
                break;
            }
            if (node.childNodes.length !== other.childNodes.length) return false;
            for (var i = node.childNodes.length - 1; i >= 0; i--) {
              stack.push({ node: node.childNodes[i], other: other.childNodes[i] });
            }
          }
          return true;
        },
        /**
         * Checks whether or not the given node is this node.
         *
         * @param {Node} [otherNode]
         */
        isSameNode: function(otherNode) {
          return this === otherNode;
        },
        /**
         * Inserts a node before a reference node as a child of this node.
         *
         * @param {Node} newChild
         * The new child node to be inserted.
         * @param {Node | null} refChild
         * The reference node before which newChild will be inserted.
         * @returns {Node}
         * The new child node successfully inserted.
         * @throws {DOMException}
         * Throws a DOMException if inserting the node would result in a DOM tree that is not
         * well-formed, or if `child` is provided but is not a child of `parent`.
         * See {@link _insertBefore} for more details.
         * @since Modified in DOM L2
         */
        insertBefore: function(newChild, refChild) {
          return _insertBefore(this, newChild, refChild);
        },
        /**
         * Replaces an old child node with a new child node within this node.
         *
         * @param {Node} newChild
         * The new node that is to replace the old node.
         * If it already exists in the DOM, it is removed from its original position.
         * @param {Node} oldChild
         * The existing child node to be replaced.
         * @returns {Node}
         * Returns the replaced child node.
         * @throws {DOMException}
         * Throws a DOMException if replacing the node would result in a DOM tree that is not
         * well-formed, or if `oldChild` is not a child of `this`.
         * This can also occur if the pre-replacement validity assertion fails.
         * See {@link _insertBefore}, {@link Node.removeChild}, and
         * {@link assertPreReplacementValidityInDocument} for more details.
         * @see https://dom.spec.whatwg.org/#concept-node-replace
         */
        replaceChild: function(newChild, oldChild) {
          _insertBefore(this, newChild, oldChild, assertPreReplacementValidityInDocument);
          if (oldChild) {
            this.removeChild(oldChild);
          }
        },
        /**
         * Removes an existing child node from this node.
         *
         * @param {Node} oldChild
         * The child node to be removed.
         * @returns {Node}
         * Returns the removed child node.
         * @throws {DOMException}
         * Throws a DOMException if `oldChild` is not a child of `this`.
         * See {@link _removeChild} for more details.
         */
        removeChild: function(oldChild) {
          return _removeChild(this, oldChild);
        },
        /**
         * Appends a child node to this node.
         *
         * @param {Node} newChild
         * The child node to be appended to this node.
         * If it already exists in the DOM, it is removed from its original position.
         * @returns {Node}
         * Returns the appended child node.
         * @throws {DOMException}
         * Throws a DOMException if appending the node would result in a DOM tree that is not
         * well-formed, or if `newChild` is not a valid Node.
         * See {@link insertBefore} for more details.
         */
        appendChild: function(newChild) {
          return this.insertBefore(newChild, null);
        },
        /**
         * Determines whether this node has any child nodes.
         *
         * @returns {boolean}
         * Returns true if this node has any child nodes, and false otherwise.
         */
        hasChildNodes: function() {
          return this.firstChild != null;
        },
        /**
         * Creates a copy of the calling node.
         *
         * @param {boolean} deep
         * If true, the contents of the node are recursively copied.
         * If false, only the node itself (and its attributes, if it is an element) are copied.
         * @returns {Node}
         * Returns the newly created copy of the node.
         * @throws {DOMException}
         * May throw a DOMException if operations within {@link Element#setAttributeNode} or
         * {@link Node#appendChild} (which are potentially invoked in this method) do not meet their
         * specific constraints.
         * @see {@link cloneNode}
         */
        cloneNode: function(deep) {
          return cloneNode(this.ownerDocument || this, this, deep);
        },
        /**
         * Puts the specified node and all of its subtree into a "normalized" form. In a normalized
         * subtree, no text nodes in the subtree are empty and there are no adjacent text nodes.
         *
         * Specifically, this method merges any adjacent text nodes (i.e., nodes for which `nodeType`
         * is `TEXT_NODE`) into a single node with the combined data. It also removes any empty text
         * nodes.
         *
         * This method iterativly traverses all child nodes to normalize all descendent nodes within
         * the subtree.
         *
         * @throws {DOMException}
         * May throw a DOMException if operations within removeChild or appendData (which are
         * potentially invoked in this method) do not meet their specific constraints.
         * @since Modified in DOM Level 2
         * @see {@link Node.removeChild}
         * @see {@link CharacterData.appendData}
         * @see ../docs/walk-dom.md.
         */
        normalize: function() {
          walkDOM(this, null, {
            enter: function(node) {
              var child = node.firstChild;
              while (child) {
                var next = child.nextSibling;
                if (next !== null && next.nodeType === TEXT_NODE && child.nodeType === TEXT_NODE) {
                  var tail = [];
                  var sibling = next;
                  while (sibling !== null && sibling.nodeType === TEXT_NODE) {
                    tail.push(sibling.data);
                    sibling = sibling.nextSibling;
                  }
                  var removed = child.nextSibling;
                  while (removed !== sibling) {
                    var following = removed.nextSibling;
                    removed.parentNode = null;
                    removed.previousSibling = null;
                    removed.nextSibling = null;
                    removed = following;
                  }
                  child.nextSibling = sibling;
                  if (sibling !== null) {
                    sibling.previousSibling = child;
                  } else {
                    node.lastChild = child;
                  }
                  child.appendData(tail.join(""));
                  _onUpdateChild(node.ownerDocument, node);
                  child = sibling;
                } else {
                  child = next;
                }
              }
              return true;
            }
          });
        },
        /**
         * Checks whether the DOM implementation implements a specific feature and its version.
         *
         * @deprecated
         * Since `DOMImplementation.hasFeature` is deprecated and always returns true.
         * @param {string} feature
         * The package name of the feature to test. This is the same name that can be passed to the
         * method `hasFeature` on `DOMImplementation`.
         * @param {string} version
         * This is the version number of the package name to test.
         * @returns {boolean}
         * Returns true in all cases in the current implementation.
         * @since Introduced in DOM Level 2
         * @see {@link DOMImplementation.hasFeature}
         */
        isSupported: function(feature, version) {
          return this.ownerDocument.implementation.hasFeature(feature, version);
        },
        /**
         * Look up the prefix associated to the given namespace URI, starting from this node.
         * **The default namespace declarations are ignored by this method.**
         * See Namespace Prefix Lookup for details on the algorithm used by this method.
         *
         * **This behavior is different from the in the specs**:
         * - no node type specific handling
         * - uses the internal attribute _nsMap for resolving namespaces that is updated when changing attributes
         *
         * @param {string | null} namespaceURI
         * The namespace URI for which to find the associated prefix.
         * @returns {string | null}
         * The associated prefix, if found; otherwise, null.
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#Node3-lookupNamespacePrefix
         * @see https://www.w3.org/TR/DOM-Level-3-Core/namespaces-algorithms.html#lookupNamespacePrefixAlgo
         * @see https://dom.spec.whatwg.org/#dom-node-lookupprefix
         * @see https://github.com/xmldom/xmldom/issues/322
         * @prettierignore
         */
        lookupPrefix: function(namespaceURI) {
          var el = this;
          while (el) {
            var map = el._nsMap;
            if (map) {
              for (var n in map) {
                if (hasOwn(map, n) && map[n] === namespaceURI) {
                  return n;
                }
              }
            }
            el = el.nodeType == ATTRIBUTE_NODE ? el.ownerDocument : el.parentNode;
          }
          return null;
        },
        /**
         * This function is used to look up the namespace URI associated with the given prefix,
         * starting from this node.
         *
         * **This behavior is different from the in the specs**:
         * - no node type specific handling
         * - uses the internal attribute _nsMap for resolving namespaces that is updated when changing attributes
         *
         * @param {string | null} prefix
         * The prefix for which to find the associated namespace URI.
         * @returns {string | null}
         * The associated namespace URI, if found; otherwise, null.
         * @since DOM Level 3
         * @see https://dom.spec.whatwg.org/#dom-node-lookupnamespaceuri
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#Node3-lookupNamespaceURI
         * @prettierignore
         */
        lookupNamespaceURI: function(prefix) {
          var el = this;
          while (el) {
            var map = el._nsMap;
            if (map) {
              if (hasOwn(map, prefix)) {
                return map[prefix];
              }
            }
            el = el.nodeType == ATTRIBUTE_NODE ? el.ownerDocument : el.parentNode;
          }
          return null;
        },
        /**
         * Determines whether the given namespace URI is the default namespace.
         *
         * The function works by looking up the prefix associated with the given namespace URI. If no
         * prefix is found (i.e., the namespace URI is not registered in the namespace map of this
         * node or any of its ancestors), it returns `true`, implying the namespace URI is considered
         * the default.
         *
         * **This behavior is different from the in the specs**:
         * - no node type specific handling
         * - uses the internal attribute _nsMap for resolving namespaces that is updated when changing attributes
         *
         * @param {string | null} namespaceURI
         * The namespace URI to be checked.
         * @returns {boolean}
         * Returns true if the given namespace URI is the default namespace, false otherwise.
         * @since DOM Level 3
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#Node3-isDefaultNamespace
         * @see https://dom.spec.whatwg.org/#dom-node-isdefaultnamespace
         * @prettierignore
         */
        isDefaultNamespace: function(namespaceURI) {
          var prefix = this.lookupPrefix(namespaceURI);
          return prefix == null;
        },
        /**
         * Compares the reference node with a node with regard to their position in the document and
         * according to the document order.
         *
         * @param {Node} other
         * The node to compare the reference node to.
         * @returns {number}
         * Returns how the node is positioned relatively to the reference node according to the
         * bitmask. 0 if reference node and given node are the same.
         * @since DOM Level 3
         * @see https://www.w3.org/TR/2004/REC-DOM-Level-3-Core-20040407/core.html#Node3-compare
         * @see https://dom.spec.whatwg.org/#dom-node-comparedocumentposition
         */
        compareDocumentPosition: function(other) {
          if (this === other) return 0;
          var node1 = other;
          var node2 = this;
          var attr1 = null;
          var attr2 = null;
          if (node1 instanceof Attr) {
            attr1 = node1;
            node1 = attr1.ownerElement;
          }
          if (node2 instanceof Attr) {
            attr2 = node2;
            node2 = attr2.ownerElement;
            if (attr1 && node1 && node2 === node1) {
              for (var i = 0, attr; attr = node2.attributes[i]; i++) {
                if (attr === attr1)
                  return DocumentPosition.DOCUMENT_POSITION_IMPLEMENTATION_SPECIFIC + DocumentPosition.DOCUMENT_POSITION_PRECEDING;
                if (attr === attr2)
                  return DocumentPosition.DOCUMENT_POSITION_IMPLEMENTATION_SPECIFIC + DocumentPosition.DOCUMENT_POSITION_FOLLOWING;
              }
            }
          }
          if (!node1 || !node2 || node2.ownerDocument !== node1.ownerDocument) {
            return DocumentPosition.DOCUMENT_POSITION_DISCONNECTED + DocumentPosition.DOCUMENT_POSITION_IMPLEMENTATION_SPECIFIC + (docGUID(node2.ownerDocument) > docGUID(node1.ownerDocument) ? DocumentPosition.DOCUMENT_POSITION_FOLLOWING : DocumentPosition.DOCUMENT_POSITION_PRECEDING);
          }
          if (attr2 && node1 === node2) {
            return DocumentPosition.DOCUMENT_POSITION_CONTAINS + DocumentPosition.DOCUMENT_POSITION_PRECEDING;
          }
          if (attr1 && node1 === node2) {
            return DocumentPosition.DOCUMENT_POSITION_CONTAINED_BY + DocumentPosition.DOCUMENT_POSITION_FOLLOWING;
          }
          var chain1 = [];
          var ancestor1 = node1.parentNode;
          while (ancestor1) {
            if (!attr2 && ancestor1 === node2) {
              return DocumentPosition.DOCUMENT_POSITION_CONTAINED_BY + DocumentPosition.DOCUMENT_POSITION_FOLLOWING;
            }
            chain1.push(ancestor1);
            ancestor1 = ancestor1.parentNode;
          }
          chain1.reverse();
          var chain2 = [];
          var ancestor2 = node2.parentNode;
          while (ancestor2) {
            if (!attr1 && ancestor2 === node1) {
              return DocumentPosition.DOCUMENT_POSITION_CONTAINS + DocumentPosition.DOCUMENT_POSITION_PRECEDING;
            }
            chain2.push(ancestor2);
            ancestor2 = ancestor2.parentNode;
          }
          chain2.reverse();
          var ca = commonAncestor(chain1, chain2);
          for (var n in ca.childNodes) {
            var child = ca.childNodes[n];
            if (child === node2) return DocumentPosition.DOCUMENT_POSITION_FOLLOWING;
            if (child === node1) return DocumentPosition.DOCUMENT_POSITION_PRECEDING;
            if (chain2.indexOf(child) >= 0) return DocumentPosition.DOCUMENT_POSITION_FOLLOWING;
            if (chain1.indexOf(child) >= 0) return DocumentPosition.DOCUMENT_POSITION_PRECEDING;
          }
          return 0;
        }
      };
      function _xmlEncoder(c) {
        return c == "<" && "&lt;" || c == ">" && "&gt;" || c == "&" && "&amp;" || c == '"' && "&quot;" || "&#" + c.charCodeAt() + ";";
      }
      copy(NodeType, Node);
      copy(NodeType, Node.prototype);
      copy(DocumentPosition, Node);
      copy(DocumentPosition, Node.prototype);
      function _visitNode(node, callback) {
        walkDOM(node, null, {
          enter: function(n) {
            return callback(n) ? walkDOM.STOP : true;
          }
        });
      }
      function walkDOM(node, context, callbacks) {
        var stack = [{ node, context, phase: walkDOM.ENTER }];
        while (stack.length > 0) {
          var frame = stack.pop();
          if (frame.phase === walkDOM.ENTER) {
            var childContext = callbacks.enter(frame.node, frame.context);
            if (childContext === walkDOM.STOP) {
              return walkDOM.STOP;
            }
            stack.push({ node: frame.node, context: childContext, phase: walkDOM.EXIT });
            if (childContext === null || childContext === void 0) {
              continue;
            }
            var child = frame.node.lastChild;
            while (child) {
              stack.push({ node: child, context: childContext, phase: walkDOM.ENTER });
              child = child.previousSibling;
            }
          } else {
            if (callbacks.exit) {
              callbacks.exit(frame.node, frame.context);
            }
          }
        }
      }
      walkDOM.STOP = Symbol("walkDOM.STOP");
      walkDOM.ENTER = 0;
      walkDOM.EXIT = 1;
      function Document(symbol, options) {
        checkSymbol(symbol);
        var opt = options || {};
        this.ownerDocument = this;
        this.contentType = opt.contentType || MIME_TYPE.XML_APPLICATION;
        this.type = isHTMLMimeType(this.contentType) ? "html" : "xml";
      }
      function _onAddAttribute(doc, el, newAttr) {
        doc && doc._inc++;
        var ns = newAttr.namespaceURI;
        if (ns === NAMESPACE.XMLNS) {
          el._nsMap[newAttr.prefix ? newAttr.localName : ""] = newAttr.value;
        }
      }
      function _onRemoveAttribute(doc, el, newAttr, remove) {
        doc && doc._inc++;
        var ns = newAttr.namespaceURI;
        if (ns === NAMESPACE.XMLNS) {
          delete el._nsMap[newAttr.prefix ? newAttr.localName : ""];
        }
      }
      function _onUpdateChild(doc, parent, newChild) {
        if (doc && doc._inc) {
          doc._inc++;
          var childNodes = parent.childNodes;
          if (newChild && !newChild.nextSibling) {
            childNodes[childNodes.length++] = newChild;
          } else {
            var child = parent.firstChild;
            var i = 0;
            while (child) {
              childNodes[i++] = child;
              child = child.nextSibling;
            }
            childNodes.length = i;
            delete childNodes[childNodes.length];
          }
        }
      }
      function _removeChild(parentNode, child) {
        if (parentNode !== child.parentNode) {
          throw new DOMException2(DOMException2.NOT_FOUND_ERR, "child's parent is not parent");
        }
        var oldPreviousSibling = child.previousSibling;
        var oldNextSibling = child.nextSibling;
        if (oldPreviousSibling) {
          oldPreviousSibling.nextSibling = oldNextSibling;
        } else {
          parentNode.firstChild = oldNextSibling;
        }
        if (oldNextSibling) {
          oldNextSibling.previousSibling = oldPreviousSibling;
        } else {
          parentNode.lastChild = oldPreviousSibling;
        }
        _onUpdateChild(parentNode.ownerDocument, parentNode);
        child.parentNode = null;
        child.previousSibling = null;
        child.nextSibling = null;
        return child;
      }
      function hasValidParentNodeType(node) {
        return node && (node.nodeType === Node.DOCUMENT_NODE || node.nodeType === Node.DOCUMENT_FRAGMENT_NODE || node.nodeType === Node.ELEMENT_NODE);
      }
      function hasInsertableNodeType(node) {
        return node && (node.nodeType === Node.CDATA_SECTION_NODE || node.nodeType === Node.COMMENT_NODE || node.nodeType === Node.DOCUMENT_FRAGMENT_NODE || node.nodeType === Node.DOCUMENT_TYPE_NODE || node.nodeType === Node.ELEMENT_NODE || node.nodeType === Node.PROCESSING_INSTRUCTION_NODE || node.nodeType === Node.TEXT_NODE);
      }
      function isDocTypeNode(node) {
        return node && node.nodeType === Node.DOCUMENT_TYPE_NODE;
      }
      function isElementNode(node) {
        return node && node.nodeType === Node.ELEMENT_NODE;
      }
      function isTextNode(node) {
        return node && node.nodeType === Node.TEXT_NODE;
      }
      function isElementInsertionPossible(doc, child) {
        var parentChildNodes = doc.childNodes || [];
        if (find(parentChildNodes, isElementNode) || isDocTypeNode(child)) {
          return false;
        }
        var docTypeNode = find(parentChildNodes, isDocTypeNode);
        return !(child && docTypeNode && parentChildNodes.indexOf(docTypeNode) > parentChildNodes.indexOf(child));
      }
      function isElementReplacementPossible(doc, child) {
        var parentChildNodes = doc.childNodes || [];
        function hasElementChildThatIsNotChild(node) {
          return isElementNode(node) && node !== child;
        }
        if (find(parentChildNodes, hasElementChildThatIsNotChild)) {
          return false;
        }
        var docTypeNode = find(parentChildNodes, isDocTypeNode);
        return !(child && docTypeNode && parentChildNodes.indexOf(docTypeNode) > parentChildNodes.indexOf(child));
      }
      function assertPreInsertionValidity1to5(parent, node, child) {
        if (!hasValidParentNodeType(parent)) {
          throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Unexpected parent node type " + parent.nodeType);
        }
        if (child && child.parentNode !== parent) {
          throw new DOMException2(DOMException2.NOT_FOUND_ERR, "child not in parent");
        }
        if (
          // 4. If `node` is not a DocumentFragment, DocumentType, Element, or CharacterData node, then throw a "HierarchyRequestError" DOMException.
          !hasInsertableNodeType(node) || // 5. If either `node` is a Text node and `parent` is a document,
          // the sax parser currently adds top level text nodes, this will be fixed in 0.9.0
          // || (node.nodeType === Node.TEXT_NODE && parent.nodeType === Node.DOCUMENT_NODE)
          // or `node` is a doctype and `parent` is not a document, then throw a "HierarchyRequestError" DOMException.
          isDocTypeNode(node) && parent.nodeType !== Node.DOCUMENT_NODE
        ) {
          throw new DOMException2(
            DOMException2.HIERARCHY_REQUEST_ERR,
            "Unexpected node type " + node.nodeType + " for parent node type " + parent.nodeType
          );
        }
      }
      function assertPreInsertionValidityInDocument(parent, node, child) {
        var parentChildNodes = parent.childNodes || [];
        var nodeChildNodes = node.childNodes || [];
        if (node.nodeType === Node.DOCUMENT_FRAGMENT_NODE) {
          var nodeChildElements = nodeChildNodes.filter(isElementNode);
          if (nodeChildElements.length > 1 || find(nodeChildNodes, isTextNode)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "More than one element or text in fragment");
          }
          if (nodeChildElements.length === 1 && !isElementInsertionPossible(parent, child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Element in fragment can not be inserted before doctype");
          }
        }
        if (isElementNode(node)) {
          if (!isElementInsertionPossible(parent, child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Only one element can be added and only after doctype");
          }
        }
        if (isDocTypeNode(node)) {
          if (find(parentChildNodes, isDocTypeNode)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Only one doctype is allowed");
          }
          var parentElementChild = find(parentChildNodes, isElementNode);
          if (child && parentChildNodes.indexOf(parentElementChild) < parentChildNodes.indexOf(child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Doctype can only be inserted before an element");
          }
          if (!child && parentElementChild) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Doctype can not be appended since element is present");
          }
        }
      }
      function assertPreReplacementValidityInDocument(parent, node, child) {
        var parentChildNodes = parent.childNodes || [];
        var nodeChildNodes = node.childNodes || [];
        if (node.nodeType === Node.DOCUMENT_FRAGMENT_NODE) {
          var nodeChildElements = nodeChildNodes.filter(isElementNode);
          if (nodeChildElements.length > 1 || find(nodeChildNodes, isTextNode)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "More than one element or text in fragment");
          }
          if (nodeChildElements.length === 1 && !isElementReplacementPossible(parent, child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Element in fragment can not be inserted before doctype");
          }
        }
        if (isElementNode(node)) {
          if (!isElementReplacementPossible(parent, child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Only one element can be added and only after doctype");
          }
        }
        if (isDocTypeNode(node)) {
          let hasDoctypeChildThatIsNotChild = function(node2) {
            return isDocTypeNode(node2) && node2 !== child;
          };
          if (find(parentChildNodes, hasDoctypeChildThatIsNotChild)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Only one doctype is allowed");
          }
          var parentElementChild = find(parentChildNodes, isElementNode);
          if (child && parentChildNodes.indexOf(parentElementChild) < parentChildNodes.indexOf(child)) {
            throw new DOMException2(DOMException2.HIERARCHY_REQUEST_ERR, "Doctype can only be inserted before an element");
          }
        }
      }
      function _insertBefore(parent, node, child, _inDocumentAssertion) {
        assertPreInsertionValidity1to5(parent, node, child);
        if (parent.nodeType === Node.DOCUMENT_NODE) {
          (_inDocumentAssertion || assertPreInsertionValidityInDocument)(parent, node, child);
        }
        var cp = node.parentNode;
        if (cp) {
          cp.removeChild(node);
        }
        if (node.nodeType === DOCUMENT_FRAGMENT_NODE) {
          var newFirst = node.firstChild;
          if (newFirst == null) {
            return node;
          }
          var newLast = node.lastChild;
        } else {
          newFirst = newLast = node;
        }
        var pre = child ? child.previousSibling : parent.lastChild;
        newFirst.previousSibling = pre;
        newLast.nextSibling = child;
        if (pre) {
          pre.nextSibling = newFirst;
        } else {
          parent.firstChild = newFirst;
        }
        if (child == null) {
          parent.lastChild = newLast;
        } else {
          child.previousSibling = newLast;
        }
        do {
          newFirst.parentNode = parent;
        } while (newFirst !== newLast && (newFirst = newFirst.nextSibling));
        _onUpdateChild(parent.ownerDocument || parent, parent, node);
        if (node.nodeType == DOCUMENT_FRAGMENT_NODE) {
          node.firstChild = node.lastChild = null;
        }
        return node;
      }
      Document.prototype = {
        /**
         * The implementation that created this document.
         *
         * @type DOMImplementation
         * @readonly
         */
        implementation: null,
        nodeName: "#document",
        nodeType: DOCUMENT_NODE,
        /**
         * The DocumentType node of the document.
         *
         * @type DocumentType
         * @readonly
         */
        doctype: null,
        documentElement: null,
        _inc: 1,
        insertBefore: function(newChild, refChild) {
          if (newChild.nodeType === DOCUMENT_FRAGMENT_NODE) {
            var child = newChild.firstChild;
            while (child) {
              var next = child.nextSibling;
              this.insertBefore(child, refChild);
              child = next;
            }
            return newChild;
          }
          _insertBefore(this, newChild, refChild);
          newChild.ownerDocument = this;
          if (this.documentElement === null && newChild.nodeType === ELEMENT_NODE) {
            this.documentElement = newChild;
          }
          return newChild;
        },
        removeChild: function(oldChild) {
          var removed = _removeChild(this, oldChild);
          if (removed === this.documentElement) {
            this.documentElement = null;
          }
          return removed;
        },
        replaceChild: function(newChild, oldChild) {
          _insertBefore(this, newChild, oldChild, assertPreReplacementValidityInDocument);
          newChild.ownerDocument = this;
          if (oldChild) {
            this.removeChild(oldChild);
          }
          if (isElementNode(newChild)) {
            this.documentElement = newChild;
          }
        },
        /**
         * Imports a node from another document into this document, creating a new copy owned by this
         * document. The source node and its subtree are not modified.
         *
         * @param {Node} importedNode
         * The node to import.
         * @param {boolean} deep
         * If true, the contents of the node are recursively imported.
         * If false, only the node itself (and its attributes, if it is an element) are imported.
         * @returns {Node}
         * Returns the newly created import of the node.
         * @see {@link importNode}
         * @see {@link https://dom.spec.whatwg.org/#dom-document-importnode}
         */
        importNode: function(importedNode, deep) {
          return importNode(this, importedNode, deep);
        },
        // Introduced in DOM Level 2:
        getElementById: function(id) {
          var rtv = null;
          _visitNode(this.documentElement, function(node) {
            if (node.nodeType == ELEMENT_NODE) {
              if (node.getAttribute("id") == id) {
                rtv = node;
                return true;
              }
            }
          });
          return rtv;
        },
        /**
         * Creates a new `Element` that is owned by this `Document`.
         * In HTML Documents `localName` is the lower cased `tagName`,
         * otherwise no transformation is being applied.
         * When `contentType` implies the HTML namespace, it will be set as `namespaceURI`.
         *
         * __This implementation differs from the specification:__ - The provided name is not checked
         * against the `Name` production,
         * so no related error will be thrown.
         * - There is no interface `HTMLElement`, it is always an `Element`.
         * - There is no support for a second argument to indicate using custom elements.
         *
         * @param {string} tagName
         * @returns {Element}
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Document/createElement
         * @see https://dom.spec.whatwg.org/#dom-document-createelement
         * @see https://dom.spec.whatwg.org/#concept-create-element
         */
        createElement: function(tagName) {
          var node = new Element(PDC);
          node.ownerDocument = this;
          if (this.type === "html") {
            tagName = tagName.toLowerCase();
          }
          if (hasDefaultHTMLNamespace(this.contentType)) {
            node.namespaceURI = NAMESPACE.HTML;
          }
          node.nodeName = tagName;
          node.tagName = tagName;
          node.localName = tagName;
          node.childNodes = new NodeList();
          var attrs = node.attributes = new NamedNodeMap();
          attrs._ownerElement = node;
          return node;
        },
        /**
         * @returns {DocumentFragment}
         */
        createDocumentFragment: function() {
          var node = new DocumentFragment(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          return node;
        },
        /**
         * @param {string} data
         * @returns {Text}
         */
        createTextNode: function(data) {
          var node = new Text(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.appendData(data);
          return node;
        },
        /**
         * @param {string} data
         * @returns {Comment}
         * @see https://dom.spec.whatwg.org/#dom-document-createcomment
         * @see https://www.w3.org/TR/xml/#NT-Comment XML 1.0 production [15]
         * @see https://www.w3.org/TR/DOM-Parsing/#dfn-concept-serialize-xml §3.2.1.3
         *
         *      Note: no validation is performed at creation time. When the resulting document is
         *      serialized with `requireWellFormed: true`, the serializer throws `InvalidStateError`
         *      if the comment data contains `--` anywhere, ends with `-`, or contains characters
         *      outside the XML Char production (W3C DOM Parsing §3.2.1.3). Without that option the
         *      data is emitted verbatim.
         */
        createComment: function(data) {
          var node = new Comment(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.appendData(data);
          return node;
        },
        /**
         * Returns a new CDATASection node whose data is `data`.
         *
         * __This implementation differs from the specification:__ - calling this method on an HTML
         * document does not throw `NotSupportedError`.
         *
         * @param {string} data
         * @returns {CDATASection}
         * @throws {DOMException}
         * With code `INVALID_CHARACTER_ERR` if `data` contains `"]]>"`.
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Document/createCDATASection
         * @see https://dom.spec.whatwg.org/#dom-document-createcdatasection
         */
        createCDATASection: function(data) {
          if (data.indexOf("]]>") !== -1) {
            throw new DOMException2(DOMException2.INVALID_CHARACTER_ERR, 'data contains "]]>"');
          }
          var node = new CDATASection(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.appendData(data);
          return node;
        },
        /**
         * Returns a ProcessingInstruction node whose target is target and data is data.
         *
         * __This behavior is slightly different from the in the specs__:
         * - it does not do any input validation on the arguments and doesn't throw
         * "InvalidCharacterError".
         *
         * Note: When the resulting document is serialized with `requireWellFormed: true`, the
         * serializer throws `InvalidStateError` if `.target` is not a valid XML `NCName` (a `Name`
         * with no colon) or is an ASCII case-insensitive match for `"xml"`, or if `.data` contains
         * `?>` or characters outside the XML Char production (W3C DOM Parsing §3.2.1.7). Without that
         * option the target and data are emitted verbatim.
         *
         * @param {string} target
         * @param {string} data
         * @returns {ProcessingInstruction}
         * @see https://developer.mozilla.org/docs/Web/API/Document/createProcessingInstruction
         * @see https://dom.spec.whatwg.org/#dom-document-createprocessinginstruction
         * @see https://www.w3.org/TR/DOM-Parsing/#dfn-concept-serialize-xml §3.2.1.7
         */
        createProcessingInstruction: function(target, data) {
          var node = new ProcessingInstruction(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.nodeName = node.target = target;
          node.nodeValue = node.data = data;
          return node;
        },
        /**
         * Creates an `Attr` node that is owned by this document.
         * In HTML Documents `localName` is the lower cased `name`,
         * otherwise no transformation is being applied.
         *
         * __This implementation differs from the specification:__ - The provided name is not checked
         * against the `Name` production,
         * so no related error will be thrown.
         *
         * @param {string} name
         * @returns {Attr}
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Document/createAttribute
         * @see https://dom.spec.whatwg.org/#dom-document-createattribute
         */
        createAttribute: function(name) {
          if (!g.QName_exact.test(name)) {
            throw new DOMException2(DOMException2.INVALID_CHARACTER_ERR, 'invalid character in name "' + name + '"');
          }
          if (this.type === "html") {
            name = name.toLowerCase();
          }
          return this._createAttribute(name);
        },
        _createAttribute: function(name) {
          var node = new Attr(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.name = name;
          node.nodeName = name;
          node.localName = name;
          node.specified = true;
          return node;
        },
        /**
         * Creates an EntityReference object.
         * The current implementation does not fill the `childNodes` with those of the corresponding
         * `Entity`
         *
         * The `name` is validated against the XML `Name` production at creation time; an invalid name
         * throws `InvalidCharacterError`. When the resulting node is serialized with
         * `requireWellFormed: true`, the serializer re-validates `nodeName` against the XML `Name`
         * production and throws `InvalidStateError` if a later `nodeName` mutation made it invalid;
         * without that option the name is emitted verbatim.
         *
         * __This implementation differs from the specification:__ xmldom does not expand entities —
         * the parser resolves entity references inline and never constructs `EntityReference` nodes,
         * so this method is the only producer.
         *
         * @deprecated
         * In DOM Level 4.
         * @param {string} name
         * The name of the entity to reference. No namespace well-formedness checks are performed.
         * @returns {EntityReference}
         * @throws {DOMException}
         * With code `INVALID_CHARACTER_ERR` when `name` is not a valid XML `Name`.
         * @throws {DOMException}
         * with code `NOT_SUPPORTED_ERR` when the document is of type `html`
         * @see https://www.w3.org/TR/DOM-Level-3-Core/core.html#ID-392B75AE
         */
        createEntityReference: function(name) {
          if (!g.Name_exact.test(name)) {
            throw new DOMException2(DOMException2.INVALID_CHARACTER_ERR, 'not a valid xml name "' + name + '"');
          }
          if (this.type === "html") {
            throw new DOMException2("document is an html document", DOMExceptionName.NotSupportedError);
          }
          var node = new EntityReference(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.nodeName = name;
          return node;
        },
        // Introduced in DOM Level 2:
        /**
         * @param {string} namespaceURI
         * @param {string} qualifiedName
         * @returns {Element}
         */
        createElementNS: function(namespaceURI, qualifiedName) {
          var validated = validateAndExtract(namespaceURI, qualifiedName);
          var node = new Element(PDC);
          var attrs = node.attributes = new NamedNodeMap();
          node.childNodes = new NodeList();
          node.ownerDocument = this;
          node.nodeName = qualifiedName;
          node.tagName = qualifiedName;
          node.namespaceURI = validated[0];
          node.prefix = validated[1];
          node.localName = validated[2];
          attrs._ownerElement = node;
          return node;
        },
        // Introduced in DOM Level 2:
        /**
         * @param {string} namespaceURI
         * @param {string} qualifiedName
         * @returns {Attr}
         */
        createAttributeNS: function(namespaceURI, qualifiedName) {
          var validated = validateAndExtract(namespaceURI, qualifiedName);
          var node = new Attr(PDC);
          node.ownerDocument = this;
          node.childNodes = new NodeList();
          node.nodeName = qualifiedName;
          node.name = qualifiedName;
          node.specified = true;
          node.namespaceURI = validated[0];
          node.prefix = validated[1];
          node.localName = validated[2];
          return node;
        }
      };
      _extends(Document, Node);
      function Element(symbol) {
        checkSymbol(symbol);
        this._nsMap = /* @__PURE__ */ Object.create(null);
      }
      Element.prototype = {
        nodeType: ELEMENT_NODE,
        /**
         * The attributes of this element.
         *
         * @type {NamedNodeMap | null}
         */
        attributes: null,
        getQualifiedName: function() {
          return this.prefix ? this.prefix + ":" + this.localName : this.localName;
        },
        _isInHTMLDocumentAndNamespace: function() {
          return this.ownerDocument.type === "html" && this.namespaceURI === NAMESPACE.HTML;
        },
        /**
         * Implementaton of Level2 Core function hasAttributes.
         *
         * @returns {boolean}
         * True if attribute list is not empty.
         * @see https://www.w3.org/TR/DOM-Level-2-Core/#core-ID-NodeHasAttrs
         */
        hasAttributes: function() {
          return !!(this.attributes && this.attributes.length);
        },
        hasAttribute: function(name) {
          return !!this.getAttributeNode(name);
        },
        /**
         * Returns element’s first attribute whose qualified name is `name`, and `null`
         * if there is no such attribute.
         *
         * @param {string} name
         * @returns {string | null}
         */
        getAttribute: function(name) {
          var attr = this.getAttributeNode(name);
          return attr ? attr.value : null;
        },
        getAttributeNode: function(name) {
          if (this._isInHTMLDocumentAndNamespace()) {
            name = name.toLowerCase();
          }
          return this.attributes.getNamedItem(name);
        },
        /**
         * Sets the value of element’s first attribute whose qualified name is qualifiedName to value.
         *
         * @param {string} name
         * @param {string} value
         */
        setAttribute: function(name, value) {
          if (this._isInHTMLDocumentAndNamespace()) {
            name = name.toLowerCase();
          }
          var attr = this.getAttributeNode(name);
          if (attr) {
            attr.value = attr.nodeValue = "" + value;
          } else {
            attr = this.ownerDocument._createAttribute(name);
            attr.value = attr.nodeValue = "" + value;
            this.setAttributeNode(attr);
          }
        },
        removeAttribute: function(name) {
          var attr = this.getAttributeNode(name);
          attr && this.removeAttributeNode(attr);
        },
        setAttributeNode: function(newAttr) {
          return this.attributes.setNamedItem(newAttr);
        },
        setAttributeNodeNS: function(newAttr) {
          return this.attributes.setNamedItemNS(newAttr);
        },
        removeAttributeNode: function(oldAttr) {
          return this.attributes.removeNamedItem(oldAttr.nodeName);
        },
        //get real attribute name,and remove it by removeAttributeNode
        removeAttributeNS: function(namespaceURI, localName) {
          var old = this.getAttributeNodeNS(namespaceURI, localName);
          old && this.removeAttributeNode(old);
        },
        hasAttributeNS: function(namespaceURI, localName) {
          return this.getAttributeNodeNS(namespaceURI, localName) != null;
        },
        /**
         * Returns element’s attribute whose namespace is `namespaceURI` and local name is
         * `localName`,
         * or `null` if there is no such attribute.
         *
         * @param {string} namespaceURI
         * @param {string} localName
         * @returns {string | null}
         */
        getAttributeNS: function(namespaceURI, localName) {
          var attr = this.getAttributeNodeNS(namespaceURI, localName);
          return attr ? attr.value : null;
        },
        /**
         * Sets the value of element’s attribute whose namespace is `namespaceURI` and local name is
         * `localName` to value.
         *
         * @param {string} namespaceURI
         * @param {string} qualifiedName
         * @param {string} value
         * @see https://dom.spec.whatwg.org/#dom-element-setattributens
         */
        setAttributeNS: function(namespaceURI, qualifiedName, value) {
          var validated = validateAndExtract(namespaceURI, qualifiedName);
          var localName = validated[2];
          var attr = this.getAttributeNodeNS(namespaceURI, localName);
          if (attr) {
            attr.value = attr.nodeValue = "" + value;
          } else {
            attr = this.ownerDocument.createAttributeNS(namespaceURI, qualifiedName);
            attr.value = attr.nodeValue = "" + value;
            this.setAttributeNode(attr);
          }
        },
        getAttributeNodeNS: function(namespaceURI, localName) {
          return this.attributes.getNamedItemNS(namespaceURI, localName);
        },
        /**
         * Returns a LiveNodeList of all child elements which have **all** of the given class name(s).
         *
         * Returns an empty list if `classNames` is an empty string or only contains HTML white space
         * characters.
         *
         * Warning: This returns a live LiveNodeList.
         * Changes in the DOM will reflect in the array as the changes occur.
         * If an element selected by this array no longer qualifies for the selector,
         * it will automatically be removed. Be aware of this for iteration purposes.
         *
         * @param {string} classNames
         * Is a string representing the class name(s) to match; multiple class names are separated by
         * (ASCII-)whitespace.
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Element/getElementsByClassName
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Document/getElementsByClassName
         * @see https://dom.spec.whatwg.org/#concept-getelementsbyclassname
         */
        getElementsByClassName: function(classNames) {
          var classNamesSet = toOrderedSet(classNames);
          return new LiveNodeList(this, function(base) {
            var ls = [];
            if (classNamesSet.length > 0) {
              _visitNode(base, function(node) {
                if (node !== base && node.nodeType === ELEMENT_NODE) {
                  var nodeClassNames = node.getAttribute("class");
                  if (nodeClassNames) {
                    var matches = classNames === nodeClassNames;
                    if (!matches) {
                      var nodeClassNamesSet = toOrderedSet(nodeClassNames);
                      matches = classNamesSet.every(arrayIncludes(nodeClassNamesSet));
                    }
                    if (matches) {
                      ls.push(node);
                    }
                  }
                }
              });
            }
            return ls;
          });
        },
        /**
         * Returns a LiveNodeList of elements with the given qualifiedName.
         * Searching for all descendants can be done by passing `*` as `qualifiedName`.
         *
         * All descendants of the specified element are searched, but not the element itself.
         * The returned list is live, which means it updates itself with the DOM tree automatically.
         * Therefore, there is no need to call `Element.getElementsByTagName()`
         * with the same element and arguments repeatedly if the DOM changes in between calls.
         *
         * When called on an HTML element in an HTML document,
         * `getElementsByTagName` lower-cases the argument before searching for it.
         * This is undesirable when trying to match camel-cased SVG elements (such as
         * `<linearGradient>`) in an HTML document.
         * Instead, use `Element.getElementsByTagNameNS()`,
         * which preserves the capitalization of the tag name.
         *
         * `Element.getElementsByTagName` is similar to `Document.getElementsByTagName()`,
         * except that it only searches for elements that are descendants of the specified element.
         *
         * @param {string} qualifiedName
         * @returns {LiveNodeList}
         * @see https://developer.mozilla.org/en-US/docs/Web/API/Element/getElementsByTagName
         * @see https://dom.spec.whatwg.org/#concept-getelementsbytagname
         */
        getElementsByTagName: function(qualifiedName) {
          var isHTMLDocument = (this.nodeType === DOCUMENT_NODE ? this : this.ownerDocument).type === "html";
          var lowerQualifiedName = qualifiedName.toLowerCase();
          return new LiveNodeList(this, function(base) {
            var ls = [];
            _visitNode(base, function(node) {
              if (node === base || node.nodeType !== ELEMENT_NODE) {
                return;
              }
              if (qualifiedName === "*") {
                ls.push(node);
              } else {
                var nodeQualifiedName = node.getQualifiedName();
                var matchingQName = isHTMLDocument && node.namespaceURI === NAMESPACE.HTML ? lowerQualifiedName : qualifiedName;
                if (nodeQualifiedName === matchingQName) {
                  ls.push(node);
                }
              }
            });
            return ls;
          });
        },
        getElementsByTagNameNS: function(namespaceURI, localName) {
          return new LiveNodeList(this, function(base) {
            var ls = [];
            _visitNode(base, function(node) {
              if (node !== base && node.nodeType === ELEMENT_NODE && (namespaceURI === "*" || node.namespaceURI === namespaceURI) && (localName === "*" || node.localName == localName)) {
                ls.push(node);
              }
            });
            return ls;
          });
        }
      };
      Document.prototype.getElementsByClassName = Element.prototype.getElementsByClassName;
      Document.prototype.getElementsByTagName = Element.prototype.getElementsByTagName;
      Document.prototype.getElementsByTagNameNS = Element.prototype.getElementsByTagNameNS;
      _extends(Element, Node);
      function Attr(symbol) {
        checkSymbol(symbol);
        this.namespaceURI = null;
        this.prefix = null;
        this.ownerElement = null;
      }
      Attr.prototype.nodeType = ATTRIBUTE_NODE;
      _extends(Attr, Node);
      function CharacterData(symbol) {
        checkSymbol(symbol);
      }
      CharacterData.prototype = {
        data: "",
        substringData: function(offset, count) {
          return this.data.substring(offset, offset + count);
        },
        appendData: function(text2) {
          text2 = this.data + text2;
          this.nodeValue = this.data = text2;
          this.length = text2.length;
        },
        insertData: function(offset, text2) {
          this.replaceData(offset, 0, text2);
        },
        deleteData: function(offset, count) {
          this.replaceData(offset, count, "");
        },
        replaceData: function(offset, count, text2) {
          var start = this.data.substring(0, offset);
          var end = this.data.substring(offset + count);
          text2 = start + text2 + end;
          this.nodeValue = this.data = text2;
          this.length = text2.length;
        }
      };
      _extends(CharacterData, Node);
      function Text(symbol) {
        checkSymbol(symbol);
      }
      Text.prototype = {
        nodeName: "#text",
        nodeType: TEXT_NODE,
        splitText: function(offset) {
          var text2 = this.data;
          var newText = text2.substring(offset);
          text2 = text2.substring(0, offset);
          this.data = this.nodeValue = text2;
          this.length = text2.length;
          var newNode = this.ownerDocument.createTextNode(newText);
          if (this.parentNode) {
            this.parentNode.insertBefore(newNode, this.nextSibling);
          }
          return newNode;
        }
      };
      _extends(Text, CharacterData);
      function Comment(symbol) {
        checkSymbol(symbol);
      }
      Comment.prototype = {
        nodeName: "#comment",
        nodeType: COMMENT_NODE
      };
      _extends(Comment, CharacterData);
      function CDATASection(symbol) {
        checkSymbol(symbol);
      }
      CDATASection.prototype = {
        nodeName: "#cdata-section",
        nodeType: CDATA_SECTION_NODE
      };
      _extends(CDATASection, Text);
      function DocumentType(symbol) {
        checkSymbol(symbol);
      }
      DocumentType.prototype.nodeType = DOCUMENT_TYPE_NODE;
      _extends(DocumentType, Node);
      function Notation(symbol) {
        checkSymbol(symbol);
      }
      Notation.prototype.nodeType = NOTATION_NODE;
      _extends(Notation, Node);
      function Entity(symbol) {
        checkSymbol(symbol);
      }
      Entity.prototype.nodeType = ENTITY_NODE;
      _extends(Entity, Node);
      function EntityReference(symbol) {
        checkSymbol(symbol);
      }
      EntityReference.prototype.nodeType = ENTITY_REFERENCE_NODE;
      _extends(EntityReference, Node);
      function DocumentFragment(symbol) {
        checkSymbol(symbol);
      }
      DocumentFragment.prototype.nodeName = "#document-fragment";
      DocumentFragment.prototype.nodeType = DOCUMENT_FRAGMENT_NODE;
      _extends(DocumentFragment, Node);
      function ProcessingInstruction(symbol) {
        checkSymbol(symbol);
      }
      ProcessingInstruction.prototype.nodeType = PROCESSING_INSTRUCTION_NODE;
      _extends(ProcessingInstruction, CharacterData);
      function XMLSerializer() {
      }
      XMLSerializer.prototype.serializeToString = function(node, options) {
        return nodeSerializeToString.call(node, options);
      };
      Node.prototype.toString = nodeSerializeToString;
      function nodeSerializeToString(options) {
        var opts;
        if (typeof options === "function") {
          opts = { requireWellFormed: false, splitCDATASections: true, nodeFilter: options };
        } else if (options != null) {
          opts = {
            requireWellFormed: !!options.requireWellFormed,
            splitCDATASections: options.splitCDATASections !== false,
            nodeFilter: options.nodeFilter || null
          };
        } else {
          opts = { requireWellFormed: false, splitCDATASections: true, nodeFilter: null };
        }
        var buf = [];
        var refNode = this.nodeType === DOCUMENT_NODE && this.documentElement || this;
        var prefix = refNode.prefix;
        var uri = refNode.namespaceURI;
        if (uri && prefix == null) {
          var prefix = refNode.lookupPrefix(uri);
          if (prefix == null) {
            var visibleNamespaces = [
              { namespace: uri, prefix: null }
              //{namespace:uri,prefix:''}
            ];
          }
        }
        serializeToString(this, buf, visibleNamespaces, opts);
        return buf.join("");
      }
      function needNamespaceDefine(node, isHTML, visibleNamespaces) {
        var prefix = node.prefix || "";
        var uri = node.namespaceURI;
        if (!uri) {
          return false;
        }
        if (prefix === "xml" && uri === NAMESPACE.XML || uri === NAMESPACE.XMLNS) {
          return false;
        }
        var i = visibleNamespaces.length;
        while (i--) {
          var ns = visibleNamespaces[i];
          if (ns.prefix === prefix) {
            return ns.namespace !== uri;
          }
        }
        return true;
      }
      function addSerializedAttribute(buf, qualifiedName, value, requireWellFormed) {
        if (requireWellFormed && !g.QName_exact.test(qualifiedName)) {
          throw new DOMException2(
            'The attribute name "' + qualifiedName + '" is not a valid XML QName',
            DOMExceptionName.InvalidStateError
          );
        }
        buf.push(" ", qualifiedName, '="', value.replace(/[<>&"\t\n\r]/g, _xmlEncoder), '"');
      }
      function serializeToString(node, buf, visibleNamespaces, opts) {
        if (!visibleNamespaces) {
          visibleNamespaces = [];
        }
        var nodeFilter = opts.nodeFilter;
        var requireWellFormed = opts.requireWellFormed;
        var splitCDATASections = opts.splitCDATASections;
        var doc = node.nodeType === DOCUMENT_NODE ? node : node.ownerDocument;
        var isHTML = doc.type === "html";
        walkDOM(
          node,
          { ns: visibleNamespaces },
          {
            enter: function(n, ctx) {
              var namespaces = ctx.ns;
              if (nodeFilter) {
                n = nodeFilter(n);
                if (n) {
                  if (typeof n == "string") {
                    buf.push(n);
                    return null;
                  }
                } else {
                  return null;
                }
              }
              switch (n.nodeType) {
                case ELEMENT_NODE:
                  var attrs = n.attributes;
                  var len = attrs.length;
                  var nodeName = n.tagName;
                  var prefixedNodeName = nodeName;
                  if (!isHTML && !n.prefix && n.namespaceURI) {
                    var defaultNS;
                    for (var ai = 0; ai < attrs.length; ai++) {
                      if (attrs.item(ai).name === "xmlns") {
                        defaultNS = attrs.item(ai).value;
                        break;
                      }
                    }
                    if (!defaultNS) {
                      for (var nsi = namespaces.length - 1; nsi >= 0; nsi--) {
                        var nsEntry = namespaces[nsi];
                        if (nsEntry.prefix === "" && nsEntry.namespace === n.namespaceURI) {
                          defaultNS = nsEntry.namespace;
                          break;
                        }
                      }
                    }
                    if (defaultNS !== n.namespaceURI) {
                      for (var nsi = namespaces.length - 1; nsi >= 0; nsi--) {
                        var nsEntry = namespaces[nsi];
                        if (nsEntry.namespace === n.namespaceURI) {
                          if (nsEntry.prefix) {
                            prefixedNodeName = nsEntry.prefix + ":" + nodeName;
                          }
                          break;
                        }
                      }
                    }
                  }
                  if (requireWellFormed && !g.QName_exact.test(prefixedNodeName)) {
                    throw new DOMException2(
                      'The element name "' + prefixedNodeName + '" is not a valid XML QName',
                      DOMExceptionName.InvalidStateError
                    );
                  }
                  buf.push("<", prefixedNodeName);
                  var childNamespaces = namespaces.slice();
                  for (var i = 0; i < len; i++) {
                    var attr = attrs.item(i);
                    if (attr.prefix == "xmlns") {
                      childNamespaces.push({
                        prefix: attr.localName,
                        namespace: attr.value
                      });
                    } else if (attr.nodeName == "xmlns") {
                      childNamespaces.push({ prefix: "", namespace: attr.value });
                    }
                  }
                  for (var i = 0; i < len; i++) {
                    var attr = attrs.item(i);
                    if (needNamespaceDefine(attr, isHTML, childNamespaces)) {
                      var attrPrefix = attr.prefix || "";
                      var uri = attr.namespaceURI;
                      addSerializedAttribute(buf, attrPrefix ? "xmlns:" + attrPrefix : "xmlns", uri, requireWellFormed);
                      childNamespaces.push({ prefix: attrPrefix, namespace: uri });
                    }
                    var filteredAttr = nodeFilter ? nodeFilter(attr) : attr;
                    if (filteredAttr) {
                      if (typeof filteredAttr === "string") {
                        buf.push(filteredAttr);
                      } else {
                        addSerializedAttribute(buf, filteredAttr.name, filteredAttr.value, requireWellFormed);
                      }
                    }
                  }
                  if (nodeName === prefixedNodeName && needNamespaceDefine(n, isHTML, childNamespaces)) {
                    var nodePrefix = n.prefix || "";
                    var uri = n.namespaceURI;
                    addSerializedAttribute(buf, nodePrefix ? "xmlns:" + nodePrefix : "xmlns", uri, requireWellFormed);
                    childNamespaces.push({ prefix: nodePrefix, namespace: uri });
                  }
                  var canCloseTag = !n.firstChild;
                  if (canCloseTag && (isHTML || n.namespaceURI === NAMESPACE.HTML)) {
                    canCloseTag = isHTMLVoidElement(nodeName);
                  }
                  if (canCloseTag) {
                    buf.push("/>");
                    return null;
                  }
                  buf.push(">");
                  if (isHTML && isHTMLRawTextElement(nodeName)) {
                    var child = n.firstChild;
                    while (child) {
                      if (child.data) {
                        buf.push(child.data);
                      } else {
                        serializeToString(child, buf, childNamespaces.slice(), opts);
                      }
                      child = child.nextSibling;
                    }
                    buf.push("</", prefixedNodeName, ">");
                    return null;
                  }
                  return { ns: childNamespaces, tag: prefixedNodeName };
                case DOCUMENT_NODE:
                case DOCUMENT_FRAGMENT_NODE:
                  if (requireWellFormed && n.nodeType === DOCUMENT_NODE && n.documentElement == null) {
                    throw new DOMException2("The Document has no documentElement", DOMExceptionName.InvalidStateError);
                  }
                  return { ns: namespaces };
                case ATTRIBUTE_NODE:
                  addSerializedAttribute(buf, n.name, n.value, requireWellFormed);
                  return null;
                case TEXT_NODE:
                  if (requireWellFormed && g.InvalidChar.test(n.data)) {
                    throw new DOMException2(
                      "The Text node data contains characters outside the XML Char production",
                      DOMExceptionName.InvalidStateError
                    );
                  }
                  buf.push(n.data.replace(/[<&>]/g, _xmlEncoder));
                  return null;
                case CDATA_SECTION_NODE:
                  if (requireWellFormed && n.data.indexOf("]]>") !== -1) {
                    throw new DOMException2('The CDATASection data contains "]]>"', DOMExceptionName.InvalidStateError);
                  }
                  if (splitCDATASections) {
                    buf.push(g.CDATA_START, n.data.replace(/]]>/g, "]]]]><![CDATA[>"), g.CDATA_END);
                  } else {
                    buf.push(g.CDATA_START, n.data, g.CDATA_END);
                  }
                  return null;
                case COMMENT_NODE:
                  if (requireWellFormed) {
                    if (g.InvalidChar.test(n.data)) {
                      throw new DOMException2(
                        "The comment node data contains characters outside the XML Char production",
                        DOMExceptionName.InvalidStateError
                      );
                    }
                    if (n.data.indexOf("--") !== -1 || n.data[n.data.length - 1] === "-") {
                      throw new DOMException2(
                        'The comment node data contains "--" or ends with "-"',
                        DOMExceptionName.InvalidStateError
                      );
                    }
                  }
                  buf.push(g.COMMENT_START, n.data, g.COMMENT_END);
                  return null;
                case DOCUMENT_TYPE_NODE:
                  var pubid = n.publicId;
                  var sysid = n.systemId;
                  if (requireWellFormed) {
                    if (!g.Name_exact.test(n.name)) {
                      throw new DOMException2(
                        'The doctype name "' + n.name + '" is not a valid XML Name',
                        DOMExceptionName.InvalidStateError
                      );
                    }
                    if (pubid && !g.PubidLiteral_match.test(pubid)) {
                      throw new DOMException2("DocumentType publicId is not a valid PubidLiteral", DOMExceptionName.InvalidStateError);
                    }
                    if (sysid && sysid !== "." && !g.SystemLiteral_match.test(sysid)) {
                      throw new DOMException2("DocumentType systemId is not a valid SystemLiteral", DOMExceptionName.InvalidStateError);
                    }
                    if (n.internalSubset && n.internalSubset.indexOf("]>") !== -1) {
                      throw new DOMException2('DocumentType internalSubset contains "]>"', DOMExceptionName.InvalidStateError);
                    }
                  }
                  buf.push(g.DOCTYPE_DECL_START, " ", n.name);
                  if (pubid) {
                    buf.push(" ", g.PUBLIC, " ", pubid);
                    if (sysid && sysid !== ".") {
                      buf.push(" ", sysid);
                    }
                  } else if (sysid && sysid !== ".") {
                    buf.push(" ", g.SYSTEM, " ", sysid);
                  }
                  if (n.internalSubset) {
                    buf.push(" [", n.internalSubset, "]");
                  }
                  buf.push(">");
                  return null;
                case PROCESSING_INSTRUCTION_NODE:
                  if (requireWellFormed) {
                    if (!g.NCName_exact.test(n.target) || n.target.toLowerCase() === "xml") {
                      throw new DOMException2(
                        'The processing instruction target "' + n.target + '" is not a valid XML NCName or is reserved',
                        DOMExceptionName.InvalidStateError
                      );
                    }
                    if (g.InvalidChar.test(n.data)) {
                      throw new DOMException2(
                        "The ProcessingInstruction data contains characters outside the XML Char production",
                        DOMExceptionName.InvalidStateError
                      );
                    }
                    if (n.data.indexOf("?>") !== -1) {
                      throw new DOMException2('The ProcessingInstruction data contains "?>"', DOMExceptionName.InvalidStateError);
                    }
                  }
                  buf.push("<?", n.target, " ", n.data, "?>");
                  return null;
                case ENTITY_REFERENCE_NODE:
                  if (requireWellFormed && !g.Name_exact.test(n.nodeName)) {
                    throw new DOMException2(
                      'The entity reference name "' + n.nodeName + '" is not a valid XML Name',
                      DOMExceptionName.InvalidStateError
                    );
                  }
                  buf.push("&", n.nodeName, ";");
                  return null;
                //case ENTITY_NODE:
                //case NOTATION_NODE:
                default:
                  buf.push("??", n.nodeName);
                  return null;
              }
            },
            exit: function(n, childCtx) {
              if (childCtx && childCtx.tag) {
                buf.push("</", childCtx.tag, ">");
              }
            }
          }
        );
      }
      function importNode(doc, node, deep) {
        var destRoot;
        walkDOM(node, null, {
          enter: function(srcNode, destParent) {
            var destNode = srcNode.cloneNode(false);
            destNode.ownerDocument = doc;
            destNode.parentNode = null;
            if (destParent === null) {
              destRoot = destNode;
            } else {
              destParent.appendChild(destNode);
            }
            var shouldDeep = srcNode.nodeType === ATTRIBUTE_NODE || deep;
            return shouldDeep ? destNode : null;
          }
        });
        return destRoot;
      }
      function cloneNode(doc, node, deep) {
        var destRoot;
        walkDOM(node, null, {
          enter: function(srcNode, destParent) {
            var destNode = new srcNode.constructor(PDC);
            for (var n in srcNode) {
              if (hasOwn(srcNode, n)) {
                var v = srcNode[n];
                if (typeof v != "object") {
                  if (v != destNode[n]) {
                    destNode[n] = v;
                  }
                }
              }
            }
            if (srcNode.childNodes) {
              destNode.childNodes = new NodeList();
            }
            destNode.ownerDocument = doc;
            var shouldDeep = deep;
            switch (destNode.nodeType) {
              case ELEMENT_NODE:
                var attrs = srcNode.attributes;
                var attrs2 = destNode.attributes = new NamedNodeMap();
                var len = attrs.length;
                attrs2._ownerElement = destNode;
                for (var i = 0; i < len; i++) {
                  destNode.setAttributeNode(cloneNode(doc, attrs.item(i), true));
                }
                break;
              case ATTRIBUTE_NODE:
                shouldDeep = true;
            }
            if (destParent !== null) {
              destParent.appendChild(destNode);
            } else {
              destRoot = destNode;
            }
            return shouldDeep ? destNode : null;
          }
        });
        return destRoot;
      }
      function __set__(object, key2, value) {
        object[key2] = value;
      }
      function childrenRefresh(node) {
        var ls = [];
        var child = node.firstChild;
        while (child) {
          if (child.nodeType === ELEMENT_NODE) {
            ls.push(child);
          }
          child = child.nextSibling;
        }
        return ls;
      }
      try {
        if (Object.defineProperty) {
          Object.defineProperty(LiveNodeList.prototype, "length", {
            get: function() {
              _updateLiveList(this);
              return this.$$length;
            }
          });
          Object.defineProperty(Node.prototype, "textContent", {
            get: function() {
              if (this.nodeType === ELEMENT_NODE || this.nodeType === DOCUMENT_FRAGMENT_NODE) {
                var buf = [];
                walkDOM(this, null, {
                  enter: function(n) {
                    if (n.nodeType === ELEMENT_NODE || n.nodeType === DOCUMENT_FRAGMENT_NODE) {
                      return true;
                    }
                    if (n.nodeType === PROCESSING_INSTRUCTION_NODE || n.nodeType === COMMENT_NODE) {
                      return null;
                    }
                    buf.push(n.nodeValue);
                  }
                });
                return buf.join("");
              }
              return this.nodeValue;
            },
            set: function(data) {
              switch (this.nodeType) {
                case ELEMENT_NODE:
                case DOCUMENT_FRAGMENT_NODE:
                  while (this.firstChild) {
                    this.removeChild(this.firstChild);
                  }
                  if (data || String(data)) {
                    this.appendChild(this.ownerDocument.createTextNode(data));
                  }
                  break;
                default:
                  this.data = data;
                  this.value = data;
                  this.nodeValue = data;
              }
            }
          });
          Object.defineProperty(CharacterData.prototype, "data", {
            get: function() {
              return this._data != null ? this._data : "";
            },
            set: function(v) {
              this._data = v;
              this.length = typeof v === "string" ? v.length : 0;
            }
          });
          Object.defineProperty(CharacterData.prototype, "nodeValue", {
            get: function() {
              return this.data;
            },
            set: function(v) {
              this.data = v;
            },
            enumerable: true,
            configurable: true
          });
          Object.defineProperty(Element.prototype, "children", {
            get: function() {
              return new LiveNodeList(this, childrenRefresh);
            }
          });
          Object.defineProperty(Document.prototype, "children", {
            get: function() {
              return new LiveNodeList(this, childrenRefresh);
            }
          });
          Object.defineProperty(DocumentFragment.prototype, "children", {
            get: function() {
              return new LiveNodeList(this, childrenRefresh);
            }
          });
          __set__ = function(object, key2, value) {
            object["$$" + key2] = value;
          };
        }
      } catch (e) {
      }
      exports._updateLiveList = _updateLiveList;
      exports.Attr = Attr;
      exports.CDATASection = CDATASection;
      exports.CharacterData = CharacterData;
      exports.Comment = Comment;
      exports.Document = Document;
      exports.DocumentFragment = DocumentFragment;
      exports.DocumentType = DocumentType;
      exports.DOMImplementation = DOMImplementation;
      exports.Element = Element;
      exports.Entity = Entity;
      exports.EntityReference = EntityReference;
      exports.LiveNodeList = LiveNodeList;
      exports.NamedNodeMap = NamedNodeMap;
      exports.Node = Node;
      exports.NodeList = NodeList;
      exports.Notation = Notation;
      exports.Text = Text;
      exports.ProcessingInstruction = ProcessingInstruction;
      exports.walkDOM = walkDOM;
      exports.XMLSerializer = XMLSerializer;
    }
  });

  // node_modules/@xmldom/xmldom/lib/entities.js
  var require_entities = __commonJS({
    "node_modules/@xmldom/xmldom/lib/entities.js"(exports) {
      "use strict";
      var freeze = require_conventions().freeze;
      exports.XML_ENTITIES = freeze({
        amp: "&",
        apos: "'",
        gt: ">",
        lt: "<",
        quot: '"'
      });
      exports.HTML_ENTITIES = freeze({
        Aacute: "\xC1",
        aacute: "\xE1",
        Abreve: "\u0102",
        abreve: "\u0103",
        ac: "\u223E",
        acd: "\u223F",
        acE: "\u223E\u0333",
        Acirc: "\xC2",
        acirc: "\xE2",
        acute: "\xB4",
        Acy: "\u0410",
        acy: "\u0430",
        AElig: "\xC6",
        aelig: "\xE6",
        af: "\u2061",
        Afr: "\u{1D504}",
        afr: "\u{1D51E}",
        Agrave: "\xC0",
        agrave: "\xE0",
        alefsym: "\u2135",
        aleph: "\u2135",
        Alpha: "\u0391",
        alpha: "\u03B1",
        Amacr: "\u0100",
        amacr: "\u0101",
        amalg: "\u2A3F",
        AMP: "&",
        amp: "&",
        And: "\u2A53",
        and: "\u2227",
        andand: "\u2A55",
        andd: "\u2A5C",
        andslope: "\u2A58",
        andv: "\u2A5A",
        ang: "\u2220",
        ange: "\u29A4",
        angle: "\u2220",
        angmsd: "\u2221",
        angmsdaa: "\u29A8",
        angmsdab: "\u29A9",
        angmsdac: "\u29AA",
        angmsdad: "\u29AB",
        angmsdae: "\u29AC",
        angmsdaf: "\u29AD",
        angmsdag: "\u29AE",
        angmsdah: "\u29AF",
        angrt: "\u221F",
        angrtvb: "\u22BE",
        angrtvbd: "\u299D",
        angsph: "\u2222",
        angst: "\xC5",
        angzarr: "\u237C",
        Aogon: "\u0104",
        aogon: "\u0105",
        Aopf: "\u{1D538}",
        aopf: "\u{1D552}",
        ap: "\u2248",
        apacir: "\u2A6F",
        apE: "\u2A70",
        ape: "\u224A",
        apid: "\u224B",
        apos: "'",
        ApplyFunction: "\u2061",
        approx: "\u2248",
        approxeq: "\u224A",
        Aring: "\xC5",
        aring: "\xE5",
        Ascr: "\u{1D49C}",
        ascr: "\u{1D4B6}",
        Assign: "\u2254",
        ast: "*",
        asymp: "\u2248",
        asympeq: "\u224D",
        Atilde: "\xC3",
        atilde: "\xE3",
        Auml: "\xC4",
        auml: "\xE4",
        awconint: "\u2233",
        awint: "\u2A11",
        backcong: "\u224C",
        backepsilon: "\u03F6",
        backprime: "\u2035",
        backsim: "\u223D",
        backsimeq: "\u22CD",
        Backslash: "\u2216",
        Barv: "\u2AE7",
        barvee: "\u22BD",
        Barwed: "\u2306",
        barwed: "\u2305",
        barwedge: "\u2305",
        bbrk: "\u23B5",
        bbrktbrk: "\u23B6",
        bcong: "\u224C",
        Bcy: "\u0411",
        bcy: "\u0431",
        bdquo: "\u201E",
        becaus: "\u2235",
        Because: "\u2235",
        because: "\u2235",
        bemptyv: "\u29B0",
        bepsi: "\u03F6",
        bernou: "\u212C",
        Bernoullis: "\u212C",
        Beta: "\u0392",
        beta: "\u03B2",
        beth: "\u2136",
        between: "\u226C",
        Bfr: "\u{1D505}",
        bfr: "\u{1D51F}",
        bigcap: "\u22C2",
        bigcirc: "\u25EF",
        bigcup: "\u22C3",
        bigodot: "\u2A00",
        bigoplus: "\u2A01",
        bigotimes: "\u2A02",
        bigsqcup: "\u2A06",
        bigstar: "\u2605",
        bigtriangledown: "\u25BD",
        bigtriangleup: "\u25B3",
        biguplus: "\u2A04",
        bigvee: "\u22C1",
        bigwedge: "\u22C0",
        bkarow: "\u290D",
        blacklozenge: "\u29EB",
        blacksquare: "\u25AA",
        blacktriangle: "\u25B4",
        blacktriangledown: "\u25BE",
        blacktriangleleft: "\u25C2",
        blacktriangleright: "\u25B8",
        blank: "\u2423",
        blk12: "\u2592",
        blk14: "\u2591",
        blk34: "\u2593",
        block: "\u2588",
        bne: "=\u20E5",
        bnequiv: "\u2261\u20E5",
        bNot: "\u2AED",
        bnot: "\u2310",
        Bopf: "\u{1D539}",
        bopf: "\u{1D553}",
        bot: "\u22A5",
        bottom: "\u22A5",
        bowtie: "\u22C8",
        boxbox: "\u29C9",
        boxDL: "\u2557",
        boxDl: "\u2556",
        boxdL: "\u2555",
        boxdl: "\u2510",
        boxDR: "\u2554",
        boxDr: "\u2553",
        boxdR: "\u2552",
        boxdr: "\u250C",
        boxH: "\u2550",
        boxh: "\u2500",
        boxHD: "\u2566",
        boxHd: "\u2564",
        boxhD: "\u2565",
        boxhd: "\u252C",
        boxHU: "\u2569",
        boxHu: "\u2567",
        boxhU: "\u2568",
        boxhu: "\u2534",
        boxminus: "\u229F",
        boxplus: "\u229E",
        boxtimes: "\u22A0",
        boxUL: "\u255D",
        boxUl: "\u255C",
        boxuL: "\u255B",
        boxul: "\u2518",
        boxUR: "\u255A",
        boxUr: "\u2559",
        boxuR: "\u2558",
        boxur: "\u2514",
        boxV: "\u2551",
        boxv: "\u2502",
        boxVH: "\u256C",
        boxVh: "\u256B",
        boxvH: "\u256A",
        boxvh: "\u253C",
        boxVL: "\u2563",
        boxVl: "\u2562",
        boxvL: "\u2561",
        boxvl: "\u2524",
        boxVR: "\u2560",
        boxVr: "\u255F",
        boxvR: "\u255E",
        boxvr: "\u251C",
        bprime: "\u2035",
        Breve: "\u02D8",
        breve: "\u02D8",
        brvbar: "\xA6",
        Bscr: "\u212C",
        bscr: "\u{1D4B7}",
        bsemi: "\u204F",
        bsim: "\u223D",
        bsime: "\u22CD",
        bsol: "\\",
        bsolb: "\u29C5",
        bsolhsub: "\u27C8",
        bull: "\u2022",
        bullet: "\u2022",
        bump: "\u224E",
        bumpE: "\u2AAE",
        bumpe: "\u224F",
        Bumpeq: "\u224E",
        bumpeq: "\u224F",
        Cacute: "\u0106",
        cacute: "\u0107",
        Cap: "\u22D2",
        cap: "\u2229",
        capand: "\u2A44",
        capbrcup: "\u2A49",
        capcap: "\u2A4B",
        capcup: "\u2A47",
        capdot: "\u2A40",
        CapitalDifferentialD: "\u2145",
        caps: "\u2229\uFE00",
        caret: "\u2041",
        caron: "\u02C7",
        Cayleys: "\u212D",
        ccaps: "\u2A4D",
        Ccaron: "\u010C",
        ccaron: "\u010D",
        Ccedil: "\xC7",
        ccedil: "\xE7",
        Ccirc: "\u0108",
        ccirc: "\u0109",
        Cconint: "\u2230",
        ccups: "\u2A4C",
        ccupssm: "\u2A50",
        Cdot: "\u010A",
        cdot: "\u010B",
        cedil: "\xB8",
        Cedilla: "\xB8",
        cemptyv: "\u29B2",
        cent: "\xA2",
        CenterDot: "\xB7",
        centerdot: "\xB7",
        Cfr: "\u212D",
        cfr: "\u{1D520}",
        CHcy: "\u0427",
        chcy: "\u0447",
        check: "\u2713",
        checkmark: "\u2713",
        Chi: "\u03A7",
        chi: "\u03C7",
        cir: "\u25CB",
        circ: "\u02C6",
        circeq: "\u2257",
        circlearrowleft: "\u21BA",
        circlearrowright: "\u21BB",
        circledast: "\u229B",
        circledcirc: "\u229A",
        circleddash: "\u229D",
        CircleDot: "\u2299",
        circledR: "\xAE",
        circledS: "\u24C8",
        CircleMinus: "\u2296",
        CirclePlus: "\u2295",
        CircleTimes: "\u2297",
        cirE: "\u29C3",
        cire: "\u2257",
        cirfnint: "\u2A10",
        cirmid: "\u2AEF",
        cirscir: "\u29C2",
        ClockwiseContourIntegral: "\u2232",
        CloseCurlyDoubleQuote: "\u201D",
        CloseCurlyQuote: "\u2019",
        clubs: "\u2663",
        clubsuit: "\u2663",
        Colon: "\u2237",
        colon: ":",
        Colone: "\u2A74",
        colone: "\u2254",
        coloneq: "\u2254",
        comma: ",",
        commat: "@",
        comp: "\u2201",
        compfn: "\u2218",
        complement: "\u2201",
        complexes: "\u2102",
        cong: "\u2245",
        congdot: "\u2A6D",
        Congruent: "\u2261",
        Conint: "\u222F",
        conint: "\u222E",
        ContourIntegral: "\u222E",
        Copf: "\u2102",
        copf: "\u{1D554}",
        coprod: "\u2210",
        Coproduct: "\u2210",
        COPY: "\xA9",
        copy: "\xA9",
        copysr: "\u2117",
        CounterClockwiseContourIntegral: "\u2233",
        crarr: "\u21B5",
        Cross: "\u2A2F",
        cross: "\u2717",
        Cscr: "\u{1D49E}",
        cscr: "\u{1D4B8}",
        csub: "\u2ACF",
        csube: "\u2AD1",
        csup: "\u2AD0",
        csupe: "\u2AD2",
        ctdot: "\u22EF",
        cudarrl: "\u2938",
        cudarrr: "\u2935",
        cuepr: "\u22DE",
        cuesc: "\u22DF",
        cularr: "\u21B6",
        cularrp: "\u293D",
        Cup: "\u22D3",
        cup: "\u222A",
        cupbrcap: "\u2A48",
        CupCap: "\u224D",
        cupcap: "\u2A46",
        cupcup: "\u2A4A",
        cupdot: "\u228D",
        cupor: "\u2A45",
        cups: "\u222A\uFE00",
        curarr: "\u21B7",
        curarrm: "\u293C",
        curlyeqprec: "\u22DE",
        curlyeqsucc: "\u22DF",
        curlyvee: "\u22CE",
        curlywedge: "\u22CF",
        curren: "\xA4",
        curvearrowleft: "\u21B6",
        curvearrowright: "\u21B7",
        cuvee: "\u22CE",
        cuwed: "\u22CF",
        cwconint: "\u2232",
        cwint: "\u2231",
        cylcty: "\u232D",
        Dagger: "\u2021",
        dagger: "\u2020",
        daleth: "\u2138",
        Darr: "\u21A1",
        dArr: "\u21D3",
        darr: "\u2193",
        dash: "\u2010",
        Dashv: "\u2AE4",
        dashv: "\u22A3",
        dbkarow: "\u290F",
        dblac: "\u02DD",
        Dcaron: "\u010E",
        dcaron: "\u010F",
        Dcy: "\u0414",
        dcy: "\u0434",
        DD: "\u2145",
        dd: "\u2146",
        ddagger: "\u2021",
        ddarr: "\u21CA",
        DDotrahd: "\u2911",
        ddotseq: "\u2A77",
        deg: "\xB0",
        Del: "\u2207",
        Delta: "\u0394",
        delta: "\u03B4",
        demptyv: "\u29B1",
        dfisht: "\u297F",
        Dfr: "\u{1D507}",
        dfr: "\u{1D521}",
        dHar: "\u2965",
        dharl: "\u21C3",
        dharr: "\u21C2",
        DiacriticalAcute: "\xB4",
        DiacriticalDot: "\u02D9",
        DiacriticalDoubleAcute: "\u02DD",
        DiacriticalGrave: "`",
        DiacriticalTilde: "\u02DC",
        diam: "\u22C4",
        Diamond: "\u22C4",
        diamond: "\u22C4",
        diamondsuit: "\u2666",
        diams: "\u2666",
        die: "\xA8",
        DifferentialD: "\u2146",
        digamma: "\u03DD",
        disin: "\u22F2",
        div: "\xF7",
        divide: "\xF7",
        divideontimes: "\u22C7",
        divonx: "\u22C7",
        DJcy: "\u0402",
        djcy: "\u0452",
        dlcorn: "\u231E",
        dlcrop: "\u230D",
        dollar: "$",
        Dopf: "\u{1D53B}",
        dopf: "\u{1D555}",
        Dot: "\xA8",
        dot: "\u02D9",
        DotDot: "\u20DC",
        doteq: "\u2250",
        doteqdot: "\u2251",
        DotEqual: "\u2250",
        dotminus: "\u2238",
        dotplus: "\u2214",
        dotsquare: "\u22A1",
        doublebarwedge: "\u2306",
        DoubleContourIntegral: "\u222F",
        DoubleDot: "\xA8",
        DoubleDownArrow: "\u21D3",
        DoubleLeftArrow: "\u21D0",
        DoubleLeftRightArrow: "\u21D4",
        DoubleLeftTee: "\u2AE4",
        DoubleLongLeftArrow: "\u27F8",
        DoubleLongLeftRightArrow: "\u27FA",
        DoubleLongRightArrow: "\u27F9",
        DoubleRightArrow: "\u21D2",
        DoubleRightTee: "\u22A8",
        DoubleUpArrow: "\u21D1",
        DoubleUpDownArrow: "\u21D5",
        DoubleVerticalBar: "\u2225",
        DownArrow: "\u2193",
        Downarrow: "\u21D3",
        downarrow: "\u2193",
        DownArrowBar: "\u2913",
        DownArrowUpArrow: "\u21F5",
        DownBreve: "\u0311",
        downdownarrows: "\u21CA",
        downharpoonleft: "\u21C3",
        downharpoonright: "\u21C2",
        DownLeftRightVector: "\u2950",
        DownLeftTeeVector: "\u295E",
        DownLeftVector: "\u21BD",
        DownLeftVectorBar: "\u2956",
        DownRightTeeVector: "\u295F",
        DownRightVector: "\u21C1",
        DownRightVectorBar: "\u2957",
        DownTee: "\u22A4",
        DownTeeArrow: "\u21A7",
        drbkarow: "\u2910",
        drcorn: "\u231F",
        drcrop: "\u230C",
        Dscr: "\u{1D49F}",
        dscr: "\u{1D4B9}",
        DScy: "\u0405",
        dscy: "\u0455",
        dsol: "\u29F6",
        Dstrok: "\u0110",
        dstrok: "\u0111",
        dtdot: "\u22F1",
        dtri: "\u25BF",
        dtrif: "\u25BE",
        duarr: "\u21F5",
        duhar: "\u296F",
        dwangle: "\u29A6",
        DZcy: "\u040F",
        dzcy: "\u045F",
        dzigrarr: "\u27FF",
        Eacute: "\xC9",
        eacute: "\xE9",
        easter: "\u2A6E",
        Ecaron: "\u011A",
        ecaron: "\u011B",
        ecir: "\u2256",
        Ecirc: "\xCA",
        ecirc: "\xEA",
        ecolon: "\u2255",
        Ecy: "\u042D",
        ecy: "\u044D",
        eDDot: "\u2A77",
        Edot: "\u0116",
        eDot: "\u2251",
        edot: "\u0117",
        ee: "\u2147",
        efDot: "\u2252",
        Efr: "\u{1D508}",
        efr: "\u{1D522}",
        eg: "\u2A9A",
        Egrave: "\xC8",
        egrave: "\xE8",
        egs: "\u2A96",
        egsdot: "\u2A98",
        el: "\u2A99",
        Element: "\u2208",
        elinters: "\u23E7",
        ell: "\u2113",
        els: "\u2A95",
        elsdot: "\u2A97",
        Emacr: "\u0112",
        emacr: "\u0113",
        empty: "\u2205",
        emptyset: "\u2205",
        EmptySmallSquare: "\u25FB",
        emptyv: "\u2205",
        EmptyVerySmallSquare: "\u25AB",
        emsp: "\u2003",
        emsp13: "\u2004",
        emsp14: "\u2005",
        ENG: "\u014A",
        eng: "\u014B",
        ensp: "\u2002",
        Eogon: "\u0118",
        eogon: "\u0119",
        Eopf: "\u{1D53C}",
        eopf: "\u{1D556}",
        epar: "\u22D5",
        eparsl: "\u29E3",
        eplus: "\u2A71",
        epsi: "\u03B5",
        Epsilon: "\u0395",
        epsilon: "\u03B5",
        epsiv: "\u03F5",
        eqcirc: "\u2256",
        eqcolon: "\u2255",
        eqsim: "\u2242",
        eqslantgtr: "\u2A96",
        eqslantless: "\u2A95",
        Equal: "\u2A75",
        equals: "=",
        EqualTilde: "\u2242",
        equest: "\u225F",
        Equilibrium: "\u21CC",
        equiv: "\u2261",
        equivDD: "\u2A78",
        eqvparsl: "\u29E5",
        erarr: "\u2971",
        erDot: "\u2253",
        Escr: "\u2130",
        escr: "\u212F",
        esdot: "\u2250",
        Esim: "\u2A73",
        esim: "\u2242",
        Eta: "\u0397",
        eta: "\u03B7",
        ETH: "\xD0",
        eth: "\xF0",
        Euml: "\xCB",
        euml: "\xEB",
        euro: "\u20AC",
        excl: "!",
        exist: "\u2203",
        Exists: "\u2203",
        expectation: "\u2130",
        ExponentialE: "\u2147",
        exponentiale: "\u2147",
        fallingdotseq: "\u2252",
        Fcy: "\u0424",
        fcy: "\u0444",
        female: "\u2640",
        ffilig: "\uFB03",
        fflig: "\uFB00",
        ffllig: "\uFB04",
        Ffr: "\u{1D509}",
        ffr: "\u{1D523}",
        filig: "\uFB01",
        FilledSmallSquare: "\u25FC",
        FilledVerySmallSquare: "\u25AA",
        fjlig: "fj",
        flat: "\u266D",
        fllig: "\uFB02",
        fltns: "\u25B1",
        fnof: "\u0192",
        Fopf: "\u{1D53D}",
        fopf: "\u{1D557}",
        ForAll: "\u2200",
        forall: "\u2200",
        fork: "\u22D4",
        forkv: "\u2AD9",
        Fouriertrf: "\u2131",
        fpartint: "\u2A0D",
        frac12: "\xBD",
        frac13: "\u2153",
        frac14: "\xBC",
        frac15: "\u2155",
        frac16: "\u2159",
        frac18: "\u215B",
        frac23: "\u2154",
        frac25: "\u2156",
        frac34: "\xBE",
        frac35: "\u2157",
        frac38: "\u215C",
        frac45: "\u2158",
        frac56: "\u215A",
        frac58: "\u215D",
        frac78: "\u215E",
        frasl: "\u2044",
        frown: "\u2322",
        Fscr: "\u2131",
        fscr: "\u{1D4BB}",
        gacute: "\u01F5",
        Gamma: "\u0393",
        gamma: "\u03B3",
        Gammad: "\u03DC",
        gammad: "\u03DD",
        gap: "\u2A86",
        Gbreve: "\u011E",
        gbreve: "\u011F",
        Gcedil: "\u0122",
        Gcirc: "\u011C",
        gcirc: "\u011D",
        Gcy: "\u0413",
        gcy: "\u0433",
        Gdot: "\u0120",
        gdot: "\u0121",
        gE: "\u2267",
        ge: "\u2265",
        gEl: "\u2A8C",
        gel: "\u22DB",
        geq: "\u2265",
        geqq: "\u2267",
        geqslant: "\u2A7E",
        ges: "\u2A7E",
        gescc: "\u2AA9",
        gesdot: "\u2A80",
        gesdoto: "\u2A82",
        gesdotol: "\u2A84",
        gesl: "\u22DB\uFE00",
        gesles: "\u2A94",
        Gfr: "\u{1D50A}",
        gfr: "\u{1D524}",
        Gg: "\u22D9",
        gg: "\u226B",
        ggg: "\u22D9",
        gimel: "\u2137",
        GJcy: "\u0403",
        gjcy: "\u0453",
        gl: "\u2277",
        gla: "\u2AA5",
        glE: "\u2A92",
        glj: "\u2AA4",
        gnap: "\u2A8A",
        gnapprox: "\u2A8A",
        gnE: "\u2269",
        gne: "\u2A88",
        gneq: "\u2A88",
        gneqq: "\u2269",
        gnsim: "\u22E7",
        Gopf: "\u{1D53E}",
        gopf: "\u{1D558}",
        grave: "`",
        GreaterEqual: "\u2265",
        GreaterEqualLess: "\u22DB",
        GreaterFullEqual: "\u2267",
        GreaterGreater: "\u2AA2",
        GreaterLess: "\u2277",
        GreaterSlantEqual: "\u2A7E",
        GreaterTilde: "\u2273",
        Gscr: "\u{1D4A2}",
        gscr: "\u210A",
        gsim: "\u2273",
        gsime: "\u2A8E",
        gsiml: "\u2A90",
        Gt: "\u226B",
        GT: ">",
        gt: ">",
        gtcc: "\u2AA7",
        gtcir: "\u2A7A",
        gtdot: "\u22D7",
        gtlPar: "\u2995",
        gtquest: "\u2A7C",
        gtrapprox: "\u2A86",
        gtrarr: "\u2978",
        gtrdot: "\u22D7",
        gtreqless: "\u22DB",
        gtreqqless: "\u2A8C",
        gtrless: "\u2277",
        gtrsim: "\u2273",
        gvertneqq: "\u2269\uFE00",
        gvnE: "\u2269\uFE00",
        Hacek: "\u02C7",
        hairsp: "\u200A",
        half: "\xBD",
        hamilt: "\u210B",
        HARDcy: "\u042A",
        hardcy: "\u044A",
        hArr: "\u21D4",
        harr: "\u2194",
        harrcir: "\u2948",
        harrw: "\u21AD",
        Hat: "^",
        hbar: "\u210F",
        Hcirc: "\u0124",
        hcirc: "\u0125",
        hearts: "\u2665",
        heartsuit: "\u2665",
        hellip: "\u2026",
        hercon: "\u22B9",
        Hfr: "\u210C",
        hfr: "\u{1D525}",
        HilbertSpace: "\u210B",
        hksearow: "\u2925",
        hkswarow: "\u2926",
        hoarr: "\u21FF",
        homtht: "\u223B",
        hookleftarrow: "\u21A9",
        hookrightarrow: "\u21AA",
        Hopf: "\u210D",
        hopf: "\u{1D559}",
        horbar: "\u2015",
        HorizontalLine: "\u2500",
        Hscr: "\u210B",
        hscr: "\u{1D4BD}",
        hslash: "\u210F",
        Hstrok: "\u0126",
        hstrok: "\u0127",
        HumpDownHump: "\u224E",
        HumpEqual: "\u224F",
        hybull: "\u2043",
        hyphen: "\u2010",
        Iacute: "\xCD",
        iacute: "\xED",
        ic: "\u2063",
        Icirc: "\xCE",
        icirc: "\xEE",
        Icy: "\u0418",
        icy: "\u0438",
        Idot: "\u0130",
        IEcy: "\u0415",
        iecy: "\u0435",
        iexcl: "\xA1",
        iff: "\u21D4",
        Ifr: "\u2111",
        ifr: "\u{1D526}",
        Igrave: "\xCC",
        igrave: "\xEC",
        ii: "\u2148",
        iiiint: "\u2A0C",
        iiint: "\u222D",
        iinfin: "\u29DC",
        iiota: "\u2129",
        IJlig: "\u0132",
        ijlig: "\u0133",
        Im: "\u2111",
        Imacr: "\u012A",
        imacr: "\u012B",
        image: "\u2111",
        ImaginaryI: "\u2148",
        imagline: "\u2110",
        imagpart: "\u2111",
        imath: "\u0131",
        imof: "\u22B7",
        imped: "\u01B5",
        Implies: "\u21D2",
        in: "\u2208",
        incare: "\u2105",
        infin: "\u221E",
        infintie: "\u29DD",
        inodot: "\u0131",
        Int: "\u222C",
        int: "\u222B",
        intcal: "\u22BA",
        integers: "\u2124",
        Integral: "\u222B",
        intercal: "\u22BA",
        Intersection: "\u22C2",
        intlarhk: "\u2A17",
        intprod: "\u2A3C",
        InvisibleComma: "\u2063",
        InvisibleTimes: "\u2062",
        IOcy: "\u0401",
        iocy: "\u0451",
        Iogon: "\u012E",
        iogon: "\u012F",
        Iopf: "\u{1D540}",
        iopf: "\u{1D55A}",
        Iota: "\u0399",
        iota: "\u03B9",
        iprod: "\u2A3C",
        iquest: "\xBF",
        Iscr: "\u2110",
        iscr: "\u{1D4BE}",
        isin: "\u2208",
        isindot: "\u22F5",
        isinE: "\u22F9",
        isins: "\u22F4",
        isinsv: "\u22F3",
        isinv: "\u2208",
        it: "\u2062",
        Itilde: "\u0128",
        itilde: "\u0129",
        Iukcy: "\u0406",
        iukcy: "\u0456",
        Iuml: "\xCF",
        iuml: "\xEF",
        Jcirc: "\u0134",
        jcirc: "\u0135",
        Jcy: "\u0419",
        jcy: "\u0439",
        Jfr: "\u{1D50D}",
        jfr: "\u{1D527}",
        jmath: "\u0237",
        Jopf: "\u{1D541}",
        jopf: "\u{1D55B}",
        Jscr: "\u{1D4A5}",
        jscr: "\u{1D4BF}",
        Jsercy: "\u0408",
        jsercy: "\u0458",
        Jukcy: "\u0404",
        jukcy: "\u0454",
        Kappa: "\u039A",
        kappa: "\u03BA",
        kappav: "\u03F0",
        Kcedil: "\u0136",
        kcedil: "\u0137",
        Kcy: "\u041A",
        kcy: "\u043A",
        Kfr: "\u{1D50E}",
        kfr: "\u{1D528}",
        kgreen: "\u0138",
        KHcy: "\u0425",
        khcy: "\u0445",
        KJcy: "\u040C",
        kjcy: "\u045C",
        Kopf: "\u{1D542}",
        kopf: "\u{1D55C}",
        Kscr: "\u{1D4A6}",
        kscr: "\u{1D4C0}",
        lAarr: "\u21DA",
        Lacute: "\u0139",
        lacute: "\u013A",
        laemptyv: "\u29B4",
        lagran: "\u2112",
        Lambda: "\u039B",
        lambda: "\u03BB",
        Lang: "\u27EA",
        lang: "\u27E8",
        langd: "\u2991",
        langle: "\u27E8",
        lap: "\u2A85",
        Laplacetrf: "\u2112",
        laquo: "\xAB",
        Larr: "\u219E",
        lArr: "\u21D0",
        larr: "\u2190",
        larrb: "\u21E4",
        larrbfs: "\u291F",
        larrfs: "\u291D",
        larrhk: "\u21A9",
        larrlp: "\u21AB",
        larrpl: "\u2939",
        larrsim: "\u2973",
        larrtl: "\u21A2",
        lat: "\u2AAB",
        lAtail: "\u291B",
        latail: "\u2919",
        late: "\u2AAD",
        lates: "\u2AAD\uFE00",
        lBarr: "\u290E",
        lbarr: "\u290C",
        lbbrk: "\u2772",
        lbrace: "{",
        lbrack: "[",
        lbrke: "\u298B",
        lbrksld: "\u298F",
        lbrkslu: "\u298D",
        Lcaron: "\u013D",
        lcaron: "\u013E",
        Lcedil: "\u013B",
        lcedil: "\u013C",
        lceil: "\u2308",
        lcub: "{",
        Lcy: "\u041B",
        lcy: "\u043B",
        ldca: "\u2936",
        ldquo: "\u201C",
        ldquor: "\u201E",
        ldrdhar: "\u2967",
        ldrushar: "\u294B",
        ldsh: "\u21B2",
        lE: "\u2266",
        le: "\u2264",
        LeftAngleBracket: "\u27E8",
        LeftArrow: "\u2190",
        Leftarrow: "\u21D0",
        leftarrow: "\u2190",
        LeftArrowBar: "\u21E4",
        LeftArrowRightArrow: "\u21C6",
        leftarrowtail: "\u21A2",
        LeftCeiling: "\u2308",
        LeftDoubleBracket: "\u27E6",
        LeftDownTeeVector: "\u2961",
        LeftDownVector: "\u21C3",
        LeftDownVectorBar: "\u2959",
        LeftFloor: "\u230A",
        leftharpoondown: "\u21BD",
        leftharpoonup: "\u21BC",
        leftleftarrows: "\u21C7",
        LeftRightArrow: "\u2194",
        Leftrightarrow: "\u21D4",
        leftrightarrow: "\u2194",
        leftrightarrows: "\u21C6",
        leftrightharpoons: "\u21CB",
        leftrightsquigarrow: "\u21AD",
        LeftRightVector: "\u294E",
        LeftTee: "\u22A3",
        LeftTeeArrow: "\u21A4",
        LeftTeeVector: "\u295A",
        leftthreetimes: "\u22CB",
        LeftTriangle: "\u22B2",
        LeftTriangleBar: "\u29CF",
        LeftTriangleEqual: "\u22B4",
        LeftUpDownVector: "\u2951",
        LeftUpTeeVector: "\u2960",
        LeftUpVector: "\u21BF",
        LeftUpVectorBar: "\u2958",
        LeftVector: "\u21BC",
        LeftVectorBar: "\u2952",
        lEg: "\u2A8B",
        leg: "\u22DA",
        leq: "\u2264",
        leqq: "\u2266",
        leqslant: "\u2A7D",
        les: "\u2A7D",
        lescc: "\u2AA8",
        lesdot: "\u2A7F",
        lesdoto: "\u2A81",
        lesdotor: "\u2A83",
        lesg: "\u22DA\uFE00",
        lesges: "\u2A93",
        lessapprox: "\u2A85",
        lessdot: "\u22D6",
        lesseqgtr: "\u22DA",
        lesseqqgtr: "\u2A8B",
        LessEqualGreater: "\u22DA",
        LessFullEqual: "\u2266",
        LessGreater: "\u2276",
        lessgtr: "\u2276",
        LessLess: "\u2AA1",
        lesssim: "\u2272",
        LessSlantEqual: "\u2A7D",
        LessTilde: "\u2272",
        lfisht: "\u297C",
        lfloor: "\u230A",
        Lfr: "\u{1D50F}",
        lfr: "\u{1D529}",
        lg: "\u2276",
        lgE: "\u2A91",
        lHar: "\u2962",
        lhard: "\u21BD",
        lharu: "\u21BC",
        lharul: "\u296A",
        lhblk: "\u2584",
        LJcy: "\u0409",
        ljcy: "\u0459",
        Ll: "\u22D8",
        ll: "\u226A",
        llarr: "\u21C7",
        llcorner: "\u231E",
        Lleftarrow: "\u21DA",
        llhard: "\u296B",
        lltri: "\u25FA",
        Lmidot: "\u013F",
        lmidot: "\u0140",
        lmoust: "\u23B0",
        lmoustache: "\u23B0",
        lnap: "\u2A89",
        lnapprox: "\u2A89",
        lnE: "\u2268",
        lne: "\u2A87",
        lneq: "\u2A87",
        lneqq: "\u2268",
        lnsim: "\u22E6",
        loang: "\u27EC",
        loarr: "\u21FD",
        lobrk: "\u27E6",
        LongLeftArrow: "\u27F5",
        Longleftarrow: "\u27F8",
        longleftarrow: "\u27F5",
        LongLeftRightArrow: "\u27F7",
        Longleftrightarrow: "\u27FA",
        longleftrightarrow: "\u27F7",
        longmapsto: "\u27FC",
        LongRightArrow: "\u27F6",
        Longrightarrow: "\u27F9",
        longrightarrow: "\u27F6",
        looparrowleft: "\u21AB",
        looparrowright: "\u21AC",
        lopar: "\u2985",
        Lopf: "\u{1D543}",
        lopf: "\u{1D55D}",
        loplus: "\u2A2D",
        lotimes: "\u2A34",
        lowast: "\u2217",
        lowbar: "_",
        LowerLeftArrow: "\u2199",
        LowerRightArrow: "\u2198",
        loz: "\u25CA",
        lozenge: "\u25CA",
        lozf: "\u29EB",
        lpar: "(",
        lparlt: "\u2993",
        lrarr: "\u21C6",
        lrcorner: "\u231F",
        lrhar: "\u21CB",
        lrhard: "\u296D",
        lrm: "\u200E",
        lrtri: "\u22BF",
        lsaquo: "\u2039",
        Lscr: "\u2112",
        lscr: "\u{1D4C1}",
        Lsh: "\u21B0",
        lsh: "\u21B0",
        lsim: "\u2272",
        lsime: "\u2A8D",
        lsimg: "\u2A8F",
        lsqb: "[",
        lsquo: "\u2018",
        lsquor: "\u201A",
        Lstrok: "\u0141",
        lstrok: "\u0142",
        Lt: "\u226A",
        LT: "<",
        lt: "<",
        ltcc: "\u2AA6",
        ltcir: "\u2A79",
        ltdot: "\u22D6",
        lthree: "\u22CB",
        ltimes: "\u22C9",
        ltlarr: "\u2976",
        ltquest: "\u2A7B",
        ltri: "\u25C3",
        ltrie: "\u22B4",
        ltrif: "\u25C2",
        ltrPar: "\u2996",
        lurdshar: "\u294A",
        luruhar: "\u2966",
        lvertneqq: "\u2268\uFE00",
        lvnE: "\u2268\uFE00",
        macr: "\xAF",
        male: "\u2642",
        malt: "\u2720",
        maltese: "\u2720",
        Map: "\u2905",
        map: "\u21A6",
        mapsto: "\u21A6",
        mapstodown: "\u21A7",
        mapstoleft: "\u21A4",
        mapstoup: "\u21A5",
        marker: "\u25AE",
        mcomma: "\u2A29",
        Mcy: "\u041C",
        mcy: "\u043C",
        mdash: "\u2014",
        mDDot: "\u223A",
        measuredangle: "\u2221",
        MediumSpace: "\u205F",
        Mellintrf: "\u2133",
        Mfr: "\u{1D510}",
        mfr: "\u{1D52A}",
        mho: "\u2127",
        micro: "\xB5",
        mid: "\u2223",
        midast: "*",
        midcir: "\u2AF0",
        middot: "\xB7",
        minus: "\u2212",
        minusb: "\u229F",
        minusd: "\u2238",
        minusdu: "\u2A2A",
        MinusPlus: "\u2213",
        mlcp: "\u2ADB",
        mldr: "\u2026",
        mnplus: "\u2213",
        models: "\u22A7",
        Mopf: "\u{1D544}",
        mopf: "\u{1D55E}",
        mp: "\u2213",
        Mscr: "\u2133",
        mscr: "\u{1D4C2}",
        mstpos: "\u223E",
        Mu: "\u039C",
        mu: "\u03BC",
        multimap: "\u22B8",
        mumap: "\u22B8",
        nabla: "\u2207",
        Nacute: "\u0143",
        nacute: "\u0144",
        nang: "\u2220\u20D2",
        nap: "\u2249",
        napE: "\u2A70\u0338",
        napid: "\u224B\u0338",
        napos: "\u0149",
        napprox: "\u2249",
        natur: "\u266E",
        natural: "\u266E",
        naturals: "\u2115",
        nbsp: "\xA0",
        nbump: "\u224E\u0338",
        nbumpe: "\u224F\u0338",
        ncap: "\u2A43",
        Ncaron: "\u0147",
        ncaron: "\u0148",
        Ncedil: "\u0145",
        ncedil: "\u0146",
        ncong: "\u2247",
        ncongdot: "\u2A6D\u0338",
        ncup: "\u2A42",
        Ncy: "\u041D",
        ncy: "\u043D",
        ndash: "\u2013",
        ne: "\u2260",
        nearhk: "\u2924",
        neArr: "\u21D7",
        nearr: "\u2197",
        nearrow: "\u2197",
        nedot: "\u2250\u0338",
        NegativeMediumSpace: "\u200B",
        NegativeThickSpace: "\u200B",
        NegativeThinSpace: "\u200B",
        NegativeVeryThinSpace: "\u200B",
        nequiv: "\u2262",
        nesear: "\u2928",
        nesim: "\u2242\u0338",
        NestedGreaterGreater: "\u226B",
        NestedLessLess: "\u226A",
        NewLine: "\n",
        nexist: "\u2204",
        nexists: "\u2204",
        Nfr: "\u{1D511}",
        nfr: "\u{1D52B}",
        ngE: "\u2267\u0338",
        nge: "\u2271",
        ngeq: "\u2271",
        ngeqq: "\u2267\u0338",
        ngeqslant: "\u2A7E\u0338",
        nges: "\u2A7E\u0338",
        nGg: "\u22D9\u0338",
        ngsim: "\u2275",
        nGt: "\u226B\u20D2",
        ngt: "\u226F",
        ngtr: "\u226F",
        nGtv: "\u226B\u0338",
        nhArr: "\u21CE",
        nharr: "\u21AE",
        nhpar: "\u2AF2",
        ni: "\u220B",
        nis: "\u22FC",
        nisd: "\u22FA",
        niv: "\u220B",
        NJcy: "\u040A",
        njcy: "\u045A",
        nlArr: "\u21CD",
        nlarr: "\u219A",
        nldr: "\u2025",
        nlE: "\u2266\u0338",
        nle: "\u2270",
        nLeftarrow: "\u21CD",
        nleftarrow: "\u219A",
        nLeftrightarrow: "\u21CE",
        nleftrightarrow: "\u21AE",
        nleq: "\u2270",
        nleqq: "\u2266\u0338",
        nleqslant: "\u2A7D\u0338",
        nles: "\u2A7D\u0338",
        nless: "\u226E",
        nLl: "\u22D8\u0338",
        nlsim: "\u2274",
        nLt: "\u226A\u20D2",
        nlt: "\u226E",
        nltri: "\u22EA",
        nltrie: "\u22EC",
        nLtv: "\u226A\u0338",
        nmid: "\u2224",
        NoBreak: "\u2060",
        NonBreakingSpace: "\xA0",
        Nopf: "\u2115",
        nopf: "\u{1D55F}",
        Not: "\u2AEC",
        not: "\xAC",
        NotCongruent: "\u2262",
        NotCupCap: "\u226D",
        NotDoubleVerticalBar: "\u2226",
        NotElement: "\u2209",
        NotEqual: "\u2260",
        NotEqualTilde: "\u2242\u0338",
        NotExists: "\u2204",
        NotGreater: "\u226F",
        NotGreaterEqual: "\u2271",
        NotGreaterFullEqual: "\u2267\u0338",
        NotGreaterGreater: "\u226B\u0338",
        NotGreaterLess: "\u2279",
        NotGreaterSlantEqual: "\u2A7E\u0338",
        NotGreaterTilde: "\u2275",
        NotHumpDownHump: "\u224E\u0338",
        NotHumpEqual: "\u224F\u0338",
        notin: "\u2209",
        notindot: "\u22F5\u0338",
        notinE: "\u22F9\u0338",
        notinva: "\u2209",
        notinvb: "\u22F7",
        notinvc: "\u22F6",
        NotLeftTriangle: "\u22EA",
        NotLeftTriangleBar: "\u29CF\u0338",
        NotLeftTriangleEqual: "\u22EC",
        NotLess: "\u226E",
        NotLessEqual: "\u2270",
        NotLessGreater: "\u2278",
        NotLessLess: "\u226A\u0338",
        NotLessSlantEqual: "\u2A7D\u0338",
        NotLessTilde: "\u2274",
        NotNestedGreaterGreater: "\u2AA2\u0338",
        NotNestedLessLess: "\u2AA1\u0338",
        notni: "\u220C",
        notniva: "\u220C",
        notnivb: "\u22FE",
        notnivc: "\u22FD",
        NotPrecedes: "\u2280",
        NotPrecedesEqual: "\u2AAF\u0338",
        NotPrecedesSlantEqual: "\u22E0",
        NotReverseElement: "\u220C",
        NotRightTriangle: "\u22EB",
        NotRightTriangleBar: "\u29D0\u0338",
        NotRightTriangleEqual: "\u22ED",
        NotSquareSubset: "\u228F\u0338",
        NotSquareSubsetEqual: "\u22E2",
        NotSquareSuperset: "\u2290\u0338",
        NotSquareSupersetEqual: "\u22E3",
        NotSubset: "\u2282\u20D2",
        NotSubsetEqual: "\u2288",
        NotSucceeds: "\u2281",
        NotSucceedsEqual: "\u2AB0\u0338",
        NotSucceedsSlantEqual: "\u22E1",
        NotSucceedsTilde: "\u227F\u0338",
        NotSuperset: "\u2283\u20D2",
        NotSupersetEqual: "\u2289",
        NotTilde: "\u2241",
        NotTildeEqual: "\u2244",
        NotTildeFullEqual: "\u2247",
        NotTildeTilde: "\u2249",
        NotVerticalBar: "\u2224",
        npar: "\u2226",
        nparallel: "\u2226",
        nparsl: "\u2AFD\u20E5",
        npart: "\u2202\u0338",
        npolint: "\u2A14",
        npr: "\u2280",
        nprcue: "\u22E0",
        npre: "\u2AAF\u0338",
        nprec: "\u2280",
        npreceq: "\u2AAF\u0338",
        nrArr: "\u21CF",
        nrarr: "\u219B",
        nrarrc: "\u2933\u0338",
        nrarrw: "\u219D\u0338",
        nRightarrow: "\u21CF",
        nrightarrow: "\u219B",
        nrtri: "\u22EB",
        nrtrie: "\u22ED",
        nsc: "\u2281",
        nsccue: "\u22E1",
        nsce: "\u2AB0\u0338",
        Nscr: "\u{1D4A9}",
        nscr: "\u{1D4C3}",
        nshortmid: "\u2224",
        nshortparallel: "\u2226",
        nsim: "\u2241",
        nsime: "\u2244",
        nsimeq: "\u2244",
        nsmid: "\u2224",
        nspar: "\u2226",
        nsqsube: "\u22E2",
        nsqsupe: "\u22E3",
        nsub: "\u2284",
        nsubE: "\u2AC5\u0338",
        nsube: "\u2288",
        nsubset: "\u2282\u20D2",
        nsubseteq: "\u2288",
        nsubseteqq: "\u2AC5\u0338",
        nsucc: "\u2281",
        nsucceq: "\u2AB0\u0338",
        nsup: "\u2285",
        nsupE: "\u2AC6\u0338",
        nsupe: "\u2289",
        nsupset: "\u2283\u20D2",
        nsupseteq: "\u2289",
        nsupseteqq: "\u2AC6\u0338",
        ntgl: "\u2279",
        Ntilde: "\xD1",
        ntilde: "\xF1",
        ntlg: "\u2278",
        ntriangleleft: "\u22EA",
        ntrianglelefteq: "\u22EC",
        ntriangleright: "\u22EB",
        ntrianglerighteq: "\u22ED",
        Nu: "\u039D",
        nu: "\u03BD",
        num: "#",
        numero: "\u2116",
        numsp: "\u2007",
        nvap: "\u224D\u20D2",
        nVDash: "\u22AF",
        nVdash: "\u22AE",
        nvDash: "\u22AD",
        nvdash: "\u22AC",
        nvge: "\u2265\u20D2",
        nvgt: ">\u20D2",
        nvHarr: "\u2904",
        nvinfin: "\u29DE",
        nvlArr: "\u2902",
        nvle: "\u2264\u20D2",
        nvlt: "<\u20D2",
        nvltrie: "\u22B4\u20D2",
        nvrArr: "\u2903",
        nvrtrie: "\u22B5\u20D2",
        nvsim: "\u223C\u20D2",
        nwarhk: "\u2923",
        nwArr: "\u21D6",
        nwarr: "\u2196",
        nwarrow: "\u2196",
        nwnear: "\u2927",
        Oacute: "\xD3",
        oacute: "\xF3",
        oast: "\u229B",
        ocir: "\u229A",
        Ocirc: "\xD4",
        ocirc: "\xF4",
        Ocy: "\u041E",
        ocy: "\u043E",
        odash: "\u229D",
        Odblac: "\u0150",
        odblac: "\u0151",
        odiv: "\u2A38",
        odot: "\u2299",
        odsold: "\u29BC",
        OElig: "\u0152",
        oelig: "\u0153",
        ofcir: "\u29BF",
        Ofr: "\u{1D512}",
        ofr: "\u{1D52C}",
        ogon: "\u02DB",
        Ograve: "\xD2",
        ograve: "\xF2",
        ogt: "\u29C1",
        ohbar: "\u29B5",
        ohm: "\u03A9",
        oint: "\u222E",
        olarr: "\u21BA",
        olcir: "\u29BE",
        olcross: "\u29BB",
        oline: "\u203E",
        olt: "\u29C0",
        Omacr: "\u014C",
        omacr: "\u014D",
        Omega: "\u03A9",
        omega: "\u03C9",
        Omicron: "\u039F",
        omicron: "\u03BF",
        omid: "\u29B6",
        ominus: "\u2296",
        Oopf: "\u{1D546}",
        oopf: "\u{1D560}",
        opar: "\u29B7",
        OpenCurlyDoubleQuote: "\u201C",
        OpenCurlyQuote: "\u2018",
        operp: "\u29B9",
        oplus: "\u2295",
        Or: "\u2A54",
        or: "\u2228",
        orarr: "\u21BB",
        ord: "\u2A5D",
        order: "\u2134",
        orderof: "\u2134",
        ordf: "\xAA",
        ordm: "\xBA",
        origof: "\u22B6",
        oror: "\u2A56",
        orslope: "\u2A57",
        orv: "\u2A5B",
        oS: "\u24C8",
        Oscr: "\u{1D4AA}",
        oscr: "\u2134",
        Oslash: "\xD8",
        oslash: "\xF8",
        osol: "\u2298",
        Otilde: "\xD5",
        otilde: "\xF5",
        Otimes: "\u2A37",
        otimes: "\u2297",
        otimesas: "\u2A36",
        Ouml: "\xD6",
        ouml: "\xF6",
        ovbar: "\u233D",
        OverBar: "\u203E",
        OverBrace: "\u23DE",
        OverBracket: "\u23B4",
        OverParenthesis: "\u23DC",
        par: "\u2225",
        para: "\xB6",
        parallel: "\u2225",
        parsim: "\u2AF3",
        parsl: "\u2AFD",
        part: "\u2202",
        PartialD: "\u2202",
        Pcy: "\u041F",
        pcy: "\u043F",
        percnt: "%",
        period: ".",
        permil: "\u2030",
        perp: "\u22A5",
        pertenk: "\u2031",
        Pfr: "\u{1D513}",
        pfr: "\u{1D52D}",
        Phi: "\u03A6",
        phi: "\u03C6",
        phiv: "\u03D5",
        phmmat: "\u2133",
        phone: "\u260E",
        Pi: "\u03A0",
        pi: "\u03C0",
        pitchfork: "\u22D4",
        piv: "\u03D6",
        planck: "\u210F",
        planckh: "\u210E",
        plankv: "\u210F",
        plus: "+",
        plusacir: "\u2A23",
        plusb: "\u229E",
        pluscir: "\u2A22",
        plusdo: "\u2214",
        plusdu: "\u2A25",
        pluse: "\u2A72",
        PlusMinus: "\xB1",
        plusmn: "\xB1",
        plussim: "\u2A26",
        plustwo: "\u2A27",
        pm: "\xB1",
        Poincareplane: "\u210C",
        pointint: "\u2A15",
        Popf: "\u2119",
        popf: "\u{1D561}",
        pound: "\xA3",
        Pr: "\u2ABB",
        pr: "\u227A",
        prap: "\u2AB7",
        prcue: "\u227C",
        prE: "\u2AB3",
        pre: "\u2AAF",
        prec: "\u227A",
        precapprox: "\u2AB7",
        preccurlyeq: "\u227C",
        Precedes: "\u227A",
        PrecedesEqual: "\u2AAF",
        PrecedesSlantEqual: "\u227C",
        PrecedesTilde: "\u227E",
        preceq: "\u2AAF",
        precnapprox: "\u2AB9",
        precneqq: "\u2AB5",
        precnsim: "\u22E8",
        precsim: "\u227E",
        Prime: "\u2033",
        prime: "\u2032",
        primes: "\u2119",
        prnap: "\u2AB9",
        prnE: "\u2AB5",
        prnsim: "\u22E8",
        prod: "\u220F",
        Product: "\u220F",
        profalar: "\u232E",
        profline: "\u2312",
        profsurf: "\u2313",
        prop: "\u221D",
        Proportion: "\u2237",
        Proportional: "\u221D",
        propto: "\u221D",
        prsim: "\u227E",
        prurel: "\u22B0",
        Pscr: "\u{1D4AB}",
        pscr: "\u{1D4C5}",
        Psi: "\u03A8",
        psi: "\u03C8",
        puncsp: "\u2008",
        Qfr: "\u{1D514}",
        qfr: "\u{1D52E}",
        qint: "\u2A0C",
        Qopf: "\u211A",
        qopf: "\u{1D562}",
        qprime: "\u2057",
        Qscr: "\u{1D4AC}",
        qscr: "\u{1D4C6}",
        quaternions: "\u210D",
        quatint: "\u2A16",
        quest: "?",
        questeq: "\u225F",
        QUOT: '"',
        quot: '"',
        rAarr: "\u21DB",
        race: "\u223D\u0331",
        Racute: "\u0154",
        racute: "\u0155",
        radic: "\u221A",
        raemptyv: "\u29B3",
        Rang: "\u27EB",
        rang: "\u27E9",
        rangd: "\u2992",
        range: "\u29A5",
        rangle: "\u27E9",
        raquo: "\xBB",
        Rarr: "\u21A0",
        rArr: "\u21D2",
        rarr: "\u2192",
        rarrap: "\u2975",
        rarrb: "\u21E5",
        rarrbfs: "\u2920",
        rarrc: "\u2933",
        rarrfs: "\u291E",
        rarrhk: "\u21AA",
        rarrlp: "\u21AC",
        rarrpl: "\u2945",
        rarrsim: "\u2974",
        Rarrtl: "\u2916",
        rarrtl: "\u21A3",
        rarrw: "\u219D",
        rAtail: "\u291C",
        ratail: "\u291A",
        ratio: "\u2236",
        rationals: "\u211A",
        RBarr: "\u2910",
        rBarr: "\u290F",
        rbarr: "\u290D",
        rbbrk: "\u2773",
        rbrace: "}",
        rbrack: "]",
        rbrke: "\u298C",
        rbrksld: "\u298E",
        rbrkslu: "\u2990",
        Rcaron: "\u0158",
        rcaron: "\u0159",
        Rcedil: "\u0156",
        rcedil: "\u0157",
        rceil: "\u2309",
        rcub: "}",
        Rcy: "\u0420",
        rcy: "\u0440",
        rdca: "\u2937",
        rdldhar: "\u2969",
        rdquo: "\u201D",
        rdquor: "\u201D",
        rdsh: "\u21B3",
        Re: "\u211C",
        real: "\u211C",
        realine: "\u211B",
        realpart: "\u211C",
        reals: "\u211D",
        rect: "\u25AD",
        REG: "\xAE",
        reg: "\xAE",
        ReverseElement: "\u220B",
        ReverseEquilibrium: "\u21CB",
        ReverseUpEquilibrium: "\u296F",
        rfisht: "\u297D",
        rfloor: "\u230B",
        Rfr: "\u211C",
        rfr: "\u{1D52F}",
        rHar: "\u2964",
        rhard: "\u21C1",
        rharu: "\u21C0",
        rharul: "\u296C",
        Rho: "\u03A1",
        rho: "\u03C1",
        rhov: "\u03F1",
        RightAngleBracket: "\u27E9",
        RightArrow: "\u2192",
        Rightarrow: "\u21D2",
        rightarrow: "\u2192",
        RightArrowBar: "\u21E5",
        RightArrowLeftArrow: "\u21C4",
        rightarrowtail: "\u21A3",
        RightCeiling: "\u2309",
        RightDoubleBracket: "\u27E7",
        RightDownTeeVector: "\u295D",
        RightDownVector: "\u21C2",
        RightDownVectorBar: "\u2955",
        RightFloor: "\u230B",
        rightharpoondown: "\u21C1",
        rightharpoonup: "\u21C0",
        rightleftarrows: "\u21C4",
        rightleftharpoons: "\u21CC",
        rightrightarrows: "\u21C9",
        rightsquigarrow: "\u219D",
        RightTee: "\u22A2",
        RightTeeArrow: "\u21A6",
        RightTeeVector: "\u295B",
        rightthreetimes: "\u22CC",
        RightTriangle: "\u22B3",
        RightTriangleBar: "\u29D0",
        RightTriangleEqual: "\u22B5",
        RightUpDownVector: "\u294F",
        RightUpTeeVector: "\u295C",
        RightUpVector: "\u21BE",
        RightUpVectorBar: "\u2954",
        RightVector: "\u21C0",
        RightVectorBar: "\u2953",
        ring: "\u02DA",
        risingdotseq: "\u2253",
        rlarr: "\u21C4",
        rlhar: "\u21CC",
        rlm: "\u200F",
        rmoust: "\u23B1",
        rmoustache: "\u23B1",
        rnmid: "\u2AEE",
        roang: "\u27ED",
        roarr: "\u21FE",
        robrk: "\u27E7",
        ropar: "\u2986",
        Ropf: "\u211D",
        ropf: "\u{1D563}",
        roplus: "\u2A2E",
        rotimes: "\u2A35",
        RoundImplies: "\u2970",
        rpar: ")",
        rpargt: "\u2994",
        rppolint: "\u2A12",
        rrarr: "\u21C9",
        Rrightarrow: "\u21DB",
        rsaquo: "\u203A",
        Rscr: "\u211B",
        rscr: "\u{1D4C7}",
        Rsh: "\u21B1",
        rsh: "\u21B1",
        rsqb: "]",
        rsquo: "\u2019",
        rsquor: "\u2019",
        rthree: "\u22CC",
        rtimes: "\u22CA",
        rtri: "\u25B9",
        rtrie: "\u22B5",
        rtrif: "\u25B8",
        rtriltri: "\u29CE",
        RuleDelayed: "\u29F4",
        ruluhar: "\u2968",
        rx: "\u211E",
        Sacute: "\u015A",
        sacute: "\u015B",
        sbquo: "\u201A",
        Sc: "\u2ABC",
        sc: "\u227B",
        scap: "\u2AB8",
        Scaron: "\u0160",
        scaron: "\u0161",
        sccue: "\u227D",
        scE: "\u2AB4",
        sce: "\u2AB0",
        Scedil: "\u015E",
        scedil: "\u015F",
        Scirc: "\u015C",
        scirc: "\u015D",
        scnap: "\u2ABA",
        scnE: "\u2AB6",
        scnsim: "\u22E9",
        scpolint: "\u2A13",
        scsim: "\u227F",
        Scy: "\u0421",
        scy: "\u0441",
        sdot: "\u22C5",
        sdotb: "\u22A1",
        sdote: "\u2A66",
        searhk: "\u2925",
        seArr: "\u21D8",
        searr: "\u2198",
        searrow: "\u2198",
        sect: "\xA7",
        semi: ";",
        seswar: "\u2929",
        setminus: "\u2216",
        setmn: "\u2216",
        sext: "\u2736",
        Sfr: "\u{1D516}",
        sfr: "\u{1D530}",
        sfrown: "\u2322",
        sharp: "\u266F",
        SHCHcy: "\u0429",
        shchcy: "\u0449",
        SHcy: "\u0428",
        shcy: "\u0448",
        ShortDownArrow: "\u2193",
        ShortLeftArrow: "\u2190",
        shortmid: "\u2223",
        shortparallel: "\u2225",
        ShortRightArrow: "\u2192",
        ShortUpArrow: "\u2191",
        shy: "\xAD",
        Sigma: "\u03A3",
        sigma: "\u03C3",
        sigmaf: "\u03C2",
        sigmav: "\u03C2",
        sim: "\u223C",
        simdot: "\u2A6A",
        sime: "\u2243",
        simeq: "\u2243",
        simg: "\u2A9E",
        simgE: "\u2AA0",
        siml: "\u2A9D",
        simlE: "\u2A9F",
        simne: "\u2246",
        simplus: "\u2A24",
        simrarr: "\u2972",
        slarr: "\u2190",
        SmallCircle: "\u2218",
        smallsetminus: "\u2216",
        smashp: "\u2A33",
        smeparsl: "\u29E4",
        smid: "\u2223",
        smile: "\u2323",
        smt: "\u2AAA",
        smte: "\u2AAC",
        smtes: "\u2AAC\uFE00",
        SOFTcy: "\u042C",
        softcy: "\u044C",
        sol: "/",
        solb: "\u29C4",
        solbar: "\u233F",
        Sopf: "\u{1D54A}",
        sopf: "\u{1D564}",
        spades: "\u2660",
        spadesuit: "\u2660",
        spar: "\u2225",
        sqcap: "\u2293",
        sqcaps: "\u2293\uFE00",
        sqcup: "\u2294",
        sqcups: "\u2294\uFE00",
        Sqrt: "\u221A",
        sqsub: "\u228F",
        sqsube: "\u2291",
        sqsubset: "\u228F",
        sqsubseteq: "\u2291",
        sqsup: "\u2290",
        sqsupe: "\u2292",
        sqsupset: "\u2290",
        sqsupseteq: "\u2292",
        squ: "\u25A1",
        Square: "\u25A1",
        square: "\u25A1",
        SquareIntersection: "\u2293",
        SquareSubset: "\u228F",
        SquareSubsetEqual: "\u2291",
        SquareSuperset: "\u2290",
        SquareSupersetEqual: "\u2292",
        SquareUnion: "\u2294",
        squarf: "\u25AA",
        squf: "\u25AA",
        srarr: "\u2192",
        Sscr: "\u{1D4AE}",
        sscr: "\u{1D4C8}",
        ssetmn: "\u2216",
        ssmile: "\u2323",
        sstarf: "\u22C6",
        Star: "\u22C6",
        star: "\u2606",
        starf: "\u2605",
        straightepsilon: "\u03F5",
        straightphi: "\u03D5",
        strns: "\xAF",
        Sub: "\u22D0",
        sub: "\u2282",
        subdot: "\u2ABD",
        subE: "\u2AC5",
        sube: "\u2286",
        subedot: "\u2AC3",
        submult: "\u2AC1",
        subnE: "\u2ACB",
        subne: "\u228A",
        subplus: "\u2ABF",
        subrarr: "\u2979",
        Subset: "\u22D0",
        subset: "\u2282",
        subseteq: "\u2286",
        subseteqq: "\u2AC5",
        SubsetEqual: "\u2286",
        subsetneq: "\u228A",
        subsetneqq: "\u2ACB",
        subsim: "\u2AC7",
        subsub: "\u2AD5",
        subsup: "\u2AD3",
        succ: "\u227B",
        succapprox: "\u2AB8",
        succcurlyeq: "\u227D",
        Succeeds: "\u227B",
        SucceedsEqual: "\u2AB0",
        SucceedsSlantEqual: "\u227D",
        SucceedsTilde: "\u227F",
        succeq: "\u2AB0",
        succnapprox: "\u2ABA",
        succneqq: "\u2AB6",
        succnsim: "\u22E9",
        succsim: "\u227F",
        SuchThat: "\u220B",
        Sum: "\u2211",
        sum: "\u2211",
        sung: "\u266A",
        Sup: "\u22D1",
        sup: "\u2283",
        sup1: "\xB9",
        sup2: "\xB2",
        sup3: "\xB3",
        supdot: "\u2ABE",
        supdsub: "\u2AD8",
        supE: "\u2AC6",
        supe: "\u2287",
        supedot: "\u2AC4",
        Superset: "\u2283",
        SupersetEqual: "\u2287",
        suphsol: "\u27C9",
        suphsub: "\u2AD7",
        suplarr: "\u297B",
        supmult: "\u2AC2",
        supnE: "\u2ACC",
        supne: "\u228B",
        supplus: "\u2AC0",
        Supset: "\u22D1",
        supset: "\u2283",
        supseteq: "\u2287",
        supseteqq: "\u2AC6",
        supsetneq: "\u228B",
        supsetneqq: "\u2ACC",
        supsim: "\u2AC8",
        supsub: "\u2AD4",
        supsup: "\u2AD6",
        swarhk: "\u2926",
        swArr: "\u21D9",
        swarr: "\u2199",
        swarrow: "\u2199",
        swnwar: "\u292A",
        szlig: "\xDF",
        Tab: "	",
        target: "\u2316",
        Tau: "\u03A4",
        tau: "\u03C4",
        tbrk: "\u23B4",
        Tcaron: "\u0164",
        tcaron: "\u0165",
        Tcedil: "\u0162",
        tcedil: "\u0163",
        Tcy: "\u0422",
        tcy: "\u0442",
        tdot: "\u20DB",
        telrec: "\u2315",
        Tfr: "\u{1D517}",
        tfr: "\u{1D531}",
        there4: "\u2234",
        Therefore: "\u2234",
        therefore: "\u2234",
        Theta: "\u0398",
        theta: "\u03B8",
        thetasym: "\u03D1",
        thetav: "\u03D1",
        thickapprox: "\u2248",
        thicksim: "\u223C",
        ThickSpace: "\u205F\u200A",
        thinsp: "\u2009",
        ThinSpace: "\u2009",
        thkap: "\u2248",
        thksim: "\u223C",
        THORN: "\xDE",
        thorn: "\xFE",
        Tilde: "\u223C",
        tilde: "\u02DC",
        TildeEqual: "\u2243",
        TildeFullEqual: "\u2245",
        TildeTilde: "\u2248",
        times: "\xD7",
        timesb: "\u22A0",
        timesbar: "\u2A31",
        timesd: "\u2A30",
        tint: "\u222D",
        toea: "\u2928",
        top: "\u22A4",
        topbot: "\u2336",
        topcir: "\u2AF1",
        Topf: "\u{1D54B}",
        topf: "\u{1D565}",
        topfork: "\u2ADA",
        tosa: "\u2929",
        tprime: "\u2034",
        TRADE: "\u2122",
        trade: "\u2122",
        triangle: "\u25B5",
        triangledown: "\u25BF",
        triangleleft: "\u25C3",
        trianglelefteq: "\u22B4",
        triangleq: "\u225C",
        triangleright: "\u25B9",
        trianglerighteq: "\u22B5",
        tridot: "\u25EC",
        trie: "\u225C",
        triminus: "\u2A3A",
        TripleDot: "\u20DB",
        triplus: "\u2A39",
        trisb: "\u29CD",
        tritime: "\u2A3B",
        trpezium: "\u23E2",
        Tscr: "\u{1D4AF}",
        tscr: "\u{1D4C9}",
        TScy: "\u0426",
        tscy: "\u0446",
        TSHcy: "\u040B",
        tshcy: "\u045B",
        Tstrok: "\u0166",
        tstrok: "\u0167",
        twixt: "\u226C",
        twoheadleftarrow: "\u219E",
        twoheadrightarrow: "\u21A0",
        Uacute: "\xDA",
        uacute: "\xFA",
        Uarr: "\u219F",
        uArr: "\u21D1",
        uarr: "\u2191",
        Uarrocir: "\u2949",
        Ubrcy: "\u040E",
        ubrcy: "\u045E",
        Ubreve: "\u016C",
        ubreve: "\u016D",
        Ucirc: "\xDB",
        ucirc: "\xFB",
        Ucy: "\u0423",
        ucy: "\u0443",
        udarr: "\u21C5",
        Udblac: "\u0170",
        udblac: "\u0171",
        udhar: "\u296E",
        ufisht: "\u297E",
        Ufr: "\u{1D518}",
        ufr: "\u{1D532}",
        Ugrave: "\xD9",
        ugrave: "\xF9",
        uHar: "\u2963",
        uharl: "\u21BF",
        uharr: "\u21BE",
        uhblk: "\u2580",
        ulcorn: "\u231C",
        ulcorner: "\u231C",
        ulcrop: "\u230F",
        ultri: "\u25F8",
        Umacr: "\u016A",
        umacr: "\u016B",
        uml: "\xA8",
        UnderBar: "_",
        UnderBrace: "\u23DF",
        UnderBracket: "\u23B5",
        UnderParenthesis: "\u23DD",
        Union: "\u22C3",
        UnionPlus: "\u228E",
        Uogon: "\u0172",
        uogon: "\u0173",
        Uopf: "\u{1D54C}",
        uopf: "\u{1D566}",
        UpArrow: "\u2191",
        Uparrow: "\u21D1",
        uparrow: "\u2191",
        UpArrowBar: "\u2912",
        UpArrowDownArrow: "\u21C5",
        UpDownArrow: "\u2195",
        Updownarrow: "\u21D5",
        updownarrow: "\u2195",
        UpEquilibrium: "\u296E",
        upharpoonleft: "\u21BF",
        upharpoonright: "\u21BE",
        uplus: "\u228E",
        UpperLeftArrow: "\u2196",
        UpperRightArrow: "\u2197",
        Upsi: "\u03D2",
        upsi: "\u03C5",
        upsih: "\u03D2",
        Upsilon: "\u03A5",
        upsilon: "\u03C5",
        UpTee: "\u22A5",
        UpTeeArrow: "\u21A5",
        upuparrows: "\u21C8",
        urcorn: "\u231D",
        urcorner: "\u231D",
        urcrop: "\u230E",
        Uring: "\u016E",
        uring: "\u016F",
        urtri: "\u25F9",
        Uscr: "\u{1D4B0}",
        uscr: "\u{1D4CA}",
        utdot: "\u22F0",
        Utilde: "\u0168",
        utilde: "\u0169",
        utri: "\u25B5",
        utrif: "\u25B4",
        uuarr: "\u21C8",
        Uuml: "\xDC",
        uuml: "\xFC",
        uwangle: "\u29A7",
        vangrt: "\u299C",
        varepsilon: "\u03F5",
        varkappa: "\u03F0",
        varnothing: "\u2205",
        varphi: "\u03D5",
        varpi: "\u03D6",
        varpropto: "\u221D",
        vArr: "\u21D5",
        varr: "\u2195",
        varrho: "\u03F1",
        varsigma: "\u03C2",
        varsubsetneq: "\u228A\uFE00",
        varsubsetneqq: "\u2ACB\uFE00",
        varsupsetneq: "\u228B\uFE00",
        varsupsetneqq: "\u2ACC\uFE00",
        vartheta: "\u03D1",
        vartriangleleft: "\u22B2",
        vartriangleright: "\u22B3",
        Vbar: "\u2AEB",
        vBar: "\u2AE8",
        vBarv: "\u2AE9",
        Vcy: "\u0412",
        vcy: "\u0432",
        VDash: "\u22AB",
        Vdash: "\u22A9",
        vDash: "\u22A8",
        vdash: "\u22A2",
        Vdashl: "\u2AE6",
        Vee: "\u22C1",
        vee: "\u2228",
        veebar: "\u22BB",
        veeeq: "\u225A",
        vellip: "\u22EE",
        Verbar: "\u2016",
        verbar: "|",
        Vert: "\u2016",
        vert: "|",
        VerticalBar: "\u2223",
        VerticalLine: "|",
        VerticalSeparator: "\u2758",
        VerticalTilde: "\u2240",
        VeryThinSpace: "\u200A",
        Vfr: "\u{1D519}",
        vfr: "\u{1D533}",
        vltri: "\u22B2",
        vnsub: "\u2282\u20D2",
        vnsup: "\u2283\u20D2",
        Vopf: "\u{1D54D}",
        vopf: "\u{1D567}",
        vprop: "\u221D",
        vrtri: "\u22B3",
        Vscr: "\u{1D4B1}",
        vscr: "\u{1D4CB}",
        vsubnE: "\u2ACB\uFE00",
        vsubne: "\u228A\uFE00",
        vsupnE: "\u2ACC\uFE00",
        vsupne: "\u228B\uFE00",
        Vvdash: "\u22AA",
        vzigzag: "\u299A",
        Wcirc: "\u0174",
        wcirc: "\u0175",
        wedbar: "\u2A5F",
        Wedge: "\u22C0",
        wedge: "\u2227",
        wedgeq: "\u2259",
        weierp: "\u2118",
        Wfr: "\u{1D51A}",
        wfr: "\u{1D534}",
        Wopf: "\u{1D54E}",
        wopf: "\u{1D568}",
        wp: "\u2118",
        wr: "\u2240",
        wreath: "\u2240",
        Wscr: "\u{1D4B2}",
        wscr: "\u{1D4CC}",
        xcap: "\u22C2",
        xcirc: "\u25EF",
        xcup: "\u22C3",
        xdtri: "\u25BD",
        Xfr: "\u{1D51B}",
        xfr: "\u{1D535}",
        xhArr: "\u27FA",
        xharr: "\u27F7",
        Xi: "\u039E",
        xi: "\u03BE",
        xlArr: "\u27F8",
        xlarr: "\u27F5",
        xmap: "\u27FC",
        xnis: "\u22FB",
        xodot: "\u2A00",
        Xopf: "\u{1D54F}",
        xopf: "\u{1D569}",
        xoplus: "\u2A01",
        xotime: "\u2A02",
        xrArr: "\u27F9",
        xrarr: "\u27F6",
        Xscr: "\u{1D4B3}",
        xscr: "\u{1D4CD}",
        xsqcup: "\u2A06",
        xuplus: "\u2A04",
        xutri: "\u25B3",
        xvee: "\u22C1",
        xwedge: "\u22C0",
        Yacute: "\xDD",
        yacute: "\xFD",
        YAcy: "\u042F",
        yacy: "\u044F",
        Ycirc: "\u0176",
        ycirc: "\u0177",
        Ycy: "\u042B",
        ycy: "\u044B",
        yen: "\xA5",
        Yfr: "\u{1D51C}",
        yfr: "\u{1D536}",
        YIcy: "\u0407",
        yicy: "\u0457",
        Yopf: "\u{1D550}",
        yopf: "\u{1D56A}",
        Yscr: "\u{1D4B4}",
        yscr: "\u{1D4CE}",
        YUcy: "\u042E",
        yucy: "\u044E",
        Yuml: "\u0178",
        yuml: "\xFF",
        Zacute: "\u0179",
        zacute: "\u017A",
        Zcaron: "\u017D",
        zcaron: "\u017E",
        Zcy: "\u0417",
        zcy: "\u0437",
        Zdot: "\u017B",
        zdot: "\u017C",
        zeetrf: "\u2128",
        ZeroWidthSpace: "\u200B",
        Zeta: "\u0396",
        zeta: "\u03B6",
        Zfr: "\u2128",
        zfr: "\u{1D537}",
        ZHcy: "\u0416",
        zhcy: "\u0436",
        zigrarr: "\u21DD",
        Zopf: "\u2124",
        zopf: "\u{1D56B}",
        Zscr: "\u{1D4B5}",
        zscr: "\u{1D4CF}",
        zwj: "\u200D",
        zwnj: "\u200C"
      });
      exports.entityMap = exports.HTML_ENTITIES;
    }
  });

  // node_modules/@xmldom/xmldom/lib/sax.js
  var require_sax = __commonJS({
    "node_modules/@xmldom/xmldom/lib/sax.js"(exports) {
      "use strict";
      var conventions = require_conventions();
      var g = require_grammar();
      var errors = require_errors();
      var isHTMLEscapableRawTextElement = conventions.isHTMLEscapableRawTextElement;
      var isHTMLMimeType = conventions.isHTMLMimeType;
      var isHTMLRawTextElement = conventions.isHTMLRawTextElement;
      var hasOwn = conventions.hasOwn;
      var NAMESPACE = conventions.NAMESPACE;
      var ParseError = errors.ParseError;
      var DOMException2 = errors.DOMException;
      var S_TAG = 0;
      var S_ATTR = 1;
      var S_ATTR_SPACE = 2;
      var S_EQ = 3;
      var S_ATTR_NOQUOT_VALUE = 4;
      var S_ATTR_END = 5;
      var S_TAG_SPACE = 6;
      var S_TAG_CLOSE = 7;
      function XMLReader() {
      }
      XMLReader.prototype = {
        parse: function(source, defaultNSMap, entityMap) {
          var domBuilder = this.domBuilder;
          domBuilder.startDocument();
          _copy(defaultNSMap, defaultNSMap = /* @__PURE__ */ Object.create(null));
          parse(source, defaultNSMap, entityMap, domBuilder, this.errorHandler);
          domBuilder.endDocument();
        }
      };
      var ENTITY_REG = /&#?\w+;?/g;
      function parse(source, defaultNSMapCopy, entityMap, domBuilder, errorHandler) {
        var isHTML = isHTMLMimeType(domBuilder.mimeType);
        if (source.indexOf(g.UNICODE_REPLACEMENT_CHARACTER) >= 0) {
          errorHandler.warning("Unicode replacement character detected, source encoding issues?");
        }
        function fixedFromCharCode(code) {
          if (code > 65535) {
            code -= 65536;
            var surrogate1 = 55296 + (code >> 10), surrogate2 = 56320 + (code & 1023);
            return String.fromCharCode(surrogate1, surrogate2);
          } else {
            return String.fromCharCode(code);
          }
        }
        function entityReplacer(a2) {
          var complete = a2[a2.length - 1] === ";" ? a2 : a2 + ";";
          if (!isHTML && complete !== a2) {
            errorHandler.error("EntityRef: expecting ;");
            return a2;
          }
          var match = g.Reference.exec(complete);
          if (!match || match[0].length !== complete.length) {
            errorHandler.error("entity not matching Reference production: " + a2);
            return a2;
          }
          var k = complete.slice(1, -1);
          if (hasOwn(entityMap, k)) {
            return entityMap[k];
          } else if (k.charAt(0) === "#") {
            return fixedFromCharCode(parseInt(k.substring(1).replace("x", "0x")));
          } else {
            errorHandler.error("entity not found:" + a2);
            return a2;
          }
        }
        function appendText(end2) {
          if (end2 > start) {
            var xt = source.substring(start, end2).replace(ENTITY_REG, entityReplacer);
            locator && position(start);
            domBuilder.characters(xt, 0, end2 - start);
            start = end2;
          }
        }
        var lineStart = 0;
        var lineEnd = 0;
        var linePattern = /\r\n?|\n|$/g;
        var locator = domBuilder.locator;
        function position(p, m) {
          while (p >= lineEnd && (m = linePattern.exec(source))) {
            lineStart = lineEnd;
            lineEnd = m.index + m[0].length;
            locator.lineNumber++;
          }
          locator.columnNumber = p - lineStart + 1;
        }
        var parseStack = [{ currentNSMap: defaultNSMapCopy }];
        var unclosedTags = [];
        var start = 0;
        while (true) {
          try {
            var tagStart = source.indexOf("<", start);
            if (tagStart < 0) {
              if (!isHTML && unclosedTags.length > 0) {
                return errorHandler.fatalError("unclosed xml tag(s): " + unclosedTags.join(", "));
              }
              if (!source.substring(start).match(/^\s*$/)) {
                var doc = domBuilder.doc;
                var text2 = doc.createTextNode(source.substring(start));
                if (doc.documentElement) {
                  return errorHandler.error("Extra content at the end of the document");
                }
                doc.appendChild(text2);
                domBuilder.currentElement = text2;
              }
              return;
            }
            if (tagStart > start) {
              var fromSource = source.substring(start, tagStart);
              if (!isHTML && unclosedTags.length === 0) {
                fromSource = fromSource.replace(new RegExp(g.S_OPT.source, "g"), "");
                fromSource && errorHandler.error("Unexpected content outside root element: '" + fromSource + "'");
              }
              appendText(tagStart);
            }
            switch (source.charAt(tagStart + 1)) {
              case "/":
                var end = source.indexOf(">", tagStart + 2);
                var tagNameRaw = source.substring(tagStart + 2, end > 0 ? end : void 0);
                if (!tagNameRaw) {
                  return errorHandler.fatalError("end tag name missing");
                }
                var endTagNameStrict = g.reg("^", g.QName_group, g.S_OPT, "$");
                var tagNameMatch = end > 0 && endTagNameStrict.exec(tagNameRaw);
                if (!tagNameMatch) {
                  var leadingTagNameMatch = end > 0 && g.reg("^", g.QName_group).exec(tagNameRaw);
                  if (isHTML && leadingTagNameMatch) {
                    errorHandler.warning('end tag name contains invalid trailing characters: "' + tagNameRaw + '"');
                    tagNameMatch = leadingTagNameMatch;
                  } else if (
                    // Backward compatibility, remove this whole `else if` arm in the next breaking release
                    // (XML then falls through to the `fatalError` below, for a clean mode split: XML fatal,
                    // HTML warning). A valid end-tag name followed by a line break and trailing content was
                    // silently accepted while `reg` still used the `m` flag; re-adding `m` here matches exactly
                    // those inputs, kept recoverable and reported.
                    leadingTagNameMatch && new RegExp(endTagNameStrict.source, endTagNameStrict.flags + "m").test(tagNameRaw)
                  ) {
                    errorHandler.error('end tag name is followed by a line break and trailing content: "' + tagNameRaw + '"');
                    tagNameMatch = leadingTagNameMatch;
                  } else {
                    return errorHandler.fatalError('end tag name contains invalid characters: "' + tagNameRaw + '"');
                  }
                }
                if (!domBuilder.currentElement && !domBuilder.doc.documentElement) {
                  return;
                }
                var currentTagName = unclosedTags[unclosedTags.length - 1] || domBuilder.currentElement.tagName || domBuilder.doc.documentElement.tagName || "";
                if (currentTagName !== tagNameMatch[1]) {
                  var tagNameLower = tagNameMatch[1].toLowerCase();
                  if (!isHTML || currentTagName.toLowerCase() !== tagNameLower) {
                    return errorHandler.fatalError('Opening and ending tag mismatch: "' + currentTagName + '" != "' + tagNameRaw + '"');
                  }
                }
                var config = parseStack.pop();
                unclosedTags.pop();
                var localNSMap = config.localNSMap;
                domBuilder.endElement(config.uri, config.localName, currentTagName);
                if (localNSMap) {
                  for (var prefix in localNSMap) {
                    if (hasOwn(localNSMap, prefix)) {
                      domBuilder.endPrefixMapping(prefix);
                    }
                  }
                }
                end++;
                break;
              // end element
              case "?":
                locator && position(tagStart);
                end = parseProcessingInstruction(source, tagStart, domBuilder, errorHandler);
                break;
              case "!":
                locator && position(tagStart);
                end = parseDoctypeCommentOrCData(source, tagStart, domBuilder, errorHandler, isHTML);
                break;
              default:
                locator && position(tagStart);
                var el = new ElementAttributes();
                var currentNSMap = parseStack[parseStack.length - 1].currentNSMap;
                var end = parseElementStartPart(source, tagStart, el, currentNSMap, entityReplacer, errorHandler, isHTML);
                var len = el.length;
                if (!el.closed) {
                  if (isHTML && conventions.isHTMLVoidElement(el.tagName)) {
                    el.closed = true;
                  } else {
                    unclosedTags.push(el.tagName);
                  }
                }
                if (locator && len) {
                  var locator2 = copyLocator(locator, {});
                  for (var i = 0; i < len; i++) {
                    var a = el[i];
                    position(a.offset);
                    a.locator = copyLocator(locator, {});
                  }
                  domBuilder.locator = locator2;
                  if (appendElement(el, domBuilder, currentNSMap)) {
                    parseStack.push(el);
                  }
                  domBuilder.locator = locator;
                } else {
                  if (appendElement(el, domBuilder, currentNSMap)) {
                    parseStack.push(el);
                  }
                }
                if (isHTML && !el.closed) {
                  end = parseHtmlSpecialContent(source, end, el.tagName, entityReplacer, domBuilder);
                } else {
                  end++;
                }
            }
          } catch (e) {
            if (e instanceof ParseError) {
              throw e;
            } else if (e instanceof DOMException2) {
              return errorHandler.fatalError("Error constructing the DOM: " + e.name + ": " + e.message, e);
            }
            errorHandler.error("element parse error: " + e);
            end = -1;
          }
          if (end > start) {
            start = end;
          } else {
            appendText(Math.max(tagStart, start) + 1);
          }
        }
      }
      function copyLocator(f, t) {
        t.lineNumber = f.lineNumber;
        t.columnNumber = f.columnNumber;
        return t;
      }
      function parseElementStartPart(source, start, el, currentNSMap, entityReplacer, errorHandler, isHTML) {
        function addAttribute(qname, value2, startIndex) {
          if (hasOwn(el.attributeNames, qname)) {
            return errorHandler.fatalError("Attribute " + qname + " redefined");
          }
          if (!isHTML && value2.indexOf("<") >= 0) {
            return errorHandler.fatalError("Unescaped '<' not allowed in attributes values");
          }
          el.addValue(
            qname,
            // @see https://www.w3.org/TR/xml/#AVNormalize
            // since the xmldom sax parser does not "interpret" DTD the following is not implemented:
            // - recursive replacement of (DTD) entity references
            // - trimming and collapsing multiple spaces into a single one for attributes that are not of type CDATA
            value2.replace(/[\t\n\r]/g, " ").replace(ENTITY_REG, entityReplacer),
            startIndex
          );
        }
        var attrName;
        var value;
        var p = ++start;
        var s = S_TAG;
        while (true) {
          var c = source.charAt(p);
          if (s === S_TAG && c === "<") {
            throw new Error("unexpected < in tag name: " + source.slice(start, p));
          }
          switch (c) {
            case "=":
              if (s === S_ATTR) {
                attrName = source.slice(start, p);
                s = S_EQ;
              } else if (s === S_ATTR_SPACE) {
                s = S_EQ;
              } else {
                throw new Error("attribute equal must after attrName");
              }
              break;
            case "'":
            case '"':
              if (s === S_EQ || s === S_ATTR) {
                if (s === S_ATTR) {
                  errorHandler.warning('attribute value must after "="');
                  attrName = source.slice(start, p);
                }
                start = p + 1;
                p = source.indexOf(c, start);
                if (p > 0) {
                  value = source.slice(start, p);
                  addAttribute(attrName, value, start - 1);
                  s = S_ATTR_END;
                } else {
                  throw new Error("attribute value no end '" + c + "' match");
                }
              } else if (s == S_ATTR_NOQUOT_VALUE) {
                value = source.slice(start, p);
                addAttribute(attrName, value, start);
                errorHandler.warning('attribute "' + attrName + '" missed start quot(' + c + ")!!");
                start = p + 1;
                s = S_ATTR_END;
              } else {
                throw new Error('attribute value must after "="');
              }
              break;
            case "/":
              switch (s) {
                case S_TAG:
                  el.setTagName(source.slice(start, p));
                case S_ATTR_END:
                case S_TAG_SPACE:
                case S_TAG_CLOSE:
                  s = S_TAG_CLOSE;
                  el.closed = true;
                case S_ATTR_NOQUOT_VALUE:
                case S_ATTR:
                  break;
                case S_ATTR_SPACE:
                  el.closed = true;
                  break;
                //case S_EQ:
                default:
                  throw new Error("attribute invalid close char('/')");
              }
              break;
            case "":
              errorHandler.error("unexpected end of input");
              if (s == S_TAG) {
                el.setTagName(source.slice(start, p));
              }
              return p;
            case ">":
              switch (s) {
                case S_TAG:
                  el.setTagName(source.slice(start, p));
                case S_ATTR_END:
                case S_TAG_SPACE:
                case S_TAG_CLOSE:
                  break;
                //normal
                case S_ATTR_NOQUOT_VALUE:
                //Compatible state
                case S_ATTR:
                  value = source.slice(start, p);
                  if (value.slice(-1) === "/") {
                    el.closed = true;
                    value = value.slice(0, -1);
                  }
                case S_ATTR_SPACE:
                  if (s === S_ATTR_SPACE) {
                    value = attrName;
                  }
                  if (s == S_ATTR_NOQUOT_VALUE) {
                    errorHandler.warning('attribute "' + value + '" missed quot(")!');
                    addAttribute(attrName, value, start);
                  } else {
                    if (!isHTML) {
                      errorHandler.warning('attribute "' + value + '" missed value!! "' + value + '" instead!!');
                    }
                    addAttribute(value, value, start);
                  }
                  break;
                case S_EQ:
                  if (!isHTML) {
                    return errorHandler.fatalError(`AttValue: ' or " expected`);
                  }
              }
              return p;
            /*xml space '\x20' | #x9 | #xD | #xA; */
            case "\x80":
              c = " ";
            default:
              if (c <= " ") {
                switch (s) {
                  case S_TAG:
                    el.setTagName(source.slice(start, p));
                    s = S_TAG_SPACE;
                    break;
                  case S_ATTR:
                    attrName = source.slice(start, p);
                    s = S_ATTR_SPACE;
                    break;
                  case S_ATTR_NOQUOT_VALUE:
                    var value = source.slice(start, p);
                    errorHandler.warning('attribute "' + value + '" missed quot(")!!');
                    addAttribute(attrName, value, start);
                  case S_ATTR_END:
                    s = S_TAG_SPACE;
                    break;
                }
              } else {
                switch (s) {
                  //case S_TAG:void();break;
                  //case S_ATTR:void();break;
                  //case S_ATTR_NOQUOT_VALUE:void();break;
                  case S_ATTR_SPACE:
                    if (!isHTML) {
                      errorHandler.warning('attribute "' + attrName + '" missed value!! "' + attrName + '" instead2!!');
                    }
                    addAttribute(attrName, attrName, start);
                    start = p;
                    s = S_ATTR;
                    break;
                  case S_ATTR_END:
                    errorHandler.warning('attribute space is required"' + attrName + '"!!');
                  case S_TAG_SPACE:
                    s = S_ATTR;
                    start = p;
                    break;
                  case S_EQ:
                    s = S_ATTR_NOQUOT_VALUE;
                    start = p;
                    break;
                  case S_TAG_CLOSE:
                    throw new Error("elements closed character '/' and '>' must be connected to");
                }
              }
          }
          p++;
        }
      }
      function appendElement(el, domBuilder, currentNSMap) {
        var tagName = el.tagName;
        var localNSMap = null;
        var i = el.length;
        while (i--) {
          var a = el[i];
          var qName = a.qName;
          var value = a.value;
          var nsp = qName.indexOf(":");
          if (nsp > 0) {
            var prefix = a.prefix = qName.slice(0, nsp);
            var localName = qName.slice(nsp + 1);
            var nsPrefix = prefix === "xmlns" && localName;
          } else {
            localName = qName;
            prefix = null;
            nsPrefix = qName === "xmlns" && "";
          }
          a.localName = localName;
          if (nsPrefix !== false) {
            if (localNSMap == null) {
              localNSMap = /* @__PURE__ */ Object.create(null);
              currentNSMap = Object.create(currentNSMap);
            }
            currentNSMap[nsPrefix] = localNSMap[nsPrefix] = value;
            a.uri = NAMESPACE.XMLNS;
            domBuilder.startPrefixMapping(nsPrefix, value);
          }
        }
        var i = el.length;
        while (i--) {
          a = el[i];
          if (a.prefix) {
            if (a.prefix === "xml") {
              a.uri = NAMESPACE.XML;
            }
            if (a.prefix !== "xmlns") {
              a.uri = currentNSMap[a.prefix];
            }
          }
        }
        var nsp = tagName.indexOf(":");
        if (nsp > 0) {
          prefix = el.prefix = tagName.slice(0, nsp);
          localName = el.localName = tagName.slice(nsp + 1);
        } else {
          prefix = null;
          localName = el.localName = tagName;
        }
        var ns = el.uri = currentNSMap[prefix || ""];
        domBuilder.startElement(ns, localName, tagName, el);
        if (el.closed) {
          domBuilder.endElement(ns, localName, tagName);
          if (localNSMap) {
            for (prefix in localNSMap) {
              if (hasOwn(localNSMap, prefix)) {
                domBuilder.endPrefixMapping(prefix);
              }
            }
          }
        } else {
          el.currentNSMap = currentNSMap;
          el.localNSMap = localNSMap;
          return true;
        }
      }
      function parseHtmlSpecialContent(source, elStartEnd, tagName, entityReplacer, domBuilder) {
        var isEscapableRaw = isHTMLEscapableRawTextElement(tagName);
        if (isEscapableRaw || isHTMLRawTextElement(tagName)) {
          var closeTag = new RegExp("</" + tagName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ">", "ig");
          closeTag.lastIndex = elStartEnd;
          var match = closeTag.exec(source);
          var elEndStart = match ? match.index : -1;
          if (elEndStart < 0) {
            return elStartEnd + 1;
          }
          var text2 = source.substring(elStartEnd + 1, elEndStart);
          if (isEscapableRaw) {
            text2 = text2.replace(ENTITY_REG, entityReplacer);
          }
          domBuilder.characters(text2, 0, text2.length);
          return elEndStart;
        }
        return elStartEnd + 1;
      }
      function _copy(source, target) {
        for (var n in source) {
          if (hasOwn(source, n)) {
            target[n] = source[n];
          }
        }
      }
      function parseUtils(source, start) {
        var index = start;
        function char(n) {
          n = n || 0;
          return source.charAt(index + n);
        }
        function skip(n) {
          n = n || 1;
          index += n;
        }
        function skipBlanks() {
          var blanks = 0;
          while (index < source.length) {
            var c = char();
            if (c !== " " && c !== "\n" && c !== "	" && c !== "\r") {
              return blanks;
            }
            blanks++;
            skip();
          }
          return -1;
        }
        function substringFromIndex() {
          return source.substring(index);
        }
        function substringStartsWith(text2) {
          return source.substring(index, index + text2.length) === text2;
        }
        function substringStartsWithCaseInsensitive(text2) {
          return source.substring(index, index + text2.length).toUpperCase() === text2.toUpperCase();
        }
        function getMatch(args) {
          var expr = g.reg("^", args);
          var match = expr.exec(substringFromIndex());
          if (match) {
            skip(match[0].length);
            return match[0];
          }
          return null;
        }
        return {
          char,
          getIndex: function() {
            return index;
          },
          getMatch,
          getSource: function() {
            return source;
          },
          skip,
          skipBlanks,
          substringFromIndex,
          substringStartsWith,
          substringStartsWithCaseInsensitive
        };
      }
      function parseDoctypeInternalSubset(p, errorHandler) {
        function parsePI(p2, errorHandler2) {
          var match = g.PI.exec(p2.substringFromIndex());
          if (!match) {
            return errorHandler2.fatalError("processing instruction is not well-formed at position " + p2.getIndex());
          }
          if (match[1].toLowerCase() === "xml") {
            return errorHandler2.fatalError(
              "xml declaration is only allowed at the start of the document, but found at position " + p2.getIndex()
            );
          }
          p2.skip(match[0].length);
          return match[0];
        }
        var source = p.getSource();
        if (p.char() === "[") {
          p.skip(1);
          var intSubsetStart = p.getIndex();
          while (p.getIndex() < source.length) {
            p.skipBlanks();
            if (p.char() === "]") {
              var internalSubset = source.substring(intSubsetStart, p.getIndex());
              p.skip(1);
              return internalSubset;
            }
            var current = null;
            if (p.char() === "<" && p.char(1) === "!") {
              switch (p.char(2)) {
                case "E":
                  if (p.char(3) === "L") {
                    current = p.getMatch(g.elementdecl);
                  } else if (p.char(3) === "N") {
                    current = p.getMatch(g.EntityDecl);
                  }
                  break;
                case "A":
                  current = p.getMatch(g.AttlistDecl);
                  break;
                case "N":
                  current = p.getMatch(g.NotationDecl);
                  break;
                case "-":
                  current = p.getMatch(g.Comment);
                  break;
              }
            } else if (p.char() === "<" && p.char(1) === "?") {
              current = parsePI(p, errorHandler);
            } else if (p.char() === "%") {
              current = p.getMatch(g.PEReference);
            } else {
              return errorHandler.fatalError("Error detected in Markup declaration");
            }
            if (!current) {
              return errorHandler.fatalError("Error in internal subset at position " + p.getIndex());
            }
          }
          return errorHandler.fatalError("doctype internal subset is not well-formed, missing ]");
        }
      }
      function parseDoctypeCommentOrCData(source, start, domBuilder, errorHandler, isHTML) {
        var p = parseUtils(source, start);
        switch (isHTML ? p.char(2).toUpperCase() : p.char(2)) {
          case "-":
            var comment = p.getMatch(g.Comment);
            if (comment) {
              domBuilder.comment(comment, g.COMMENT_START.length, comment.length - g.COMMENT_START.length - g.COMMENT_END.length);
              return p.getIndex();
            } else {
              return errorHandler.fatalError("comment is not well-formed at position " + p.getIndex());
            }
          case "[":
            var cdata = p.getMatch(g.CDSect);
            if (cdata) {
              if (!isHTML && !domBuilder.currentElement) {
                return errorHandler.fatalError("CDATA outside of element");
              }
              domBuilder.startCDATA();
              domBuilder.characters(cdata, g.CDATA_START.length, cdata.length - g.CDATA_START.length - g.CDATA_END.length);
              domBuilder.endCDATA();
              return p.getIndex();
            } else {
              return errorHandler.fatalError("Invalid CDATA starting at position " + start);
            }
          case "D": {
            if (domBuilder.doc && domBuilder.doc.documentElement) {
              return errorHandler.fatalError("Doctype not allowed inside or after documentElement at position " + p.getIndex());
            }
            if (isHTML ? !p.substringStartsWithCaseInsensitive(g.DOCTYPE_DECL_START) : !p.substringStartsWith(g.DOCTYPE_DECL_START)) {
              return errorHandler.fatalError("Expected " + g.DOCTYPE_DECL_START + " at position " + p.getIndex());
            }
            p.skip(g.DOCTYPE_DECL_START.length);
            if (p.skipBlanks() < 1) {
              return errorHandler.fatalError("Expected whitespace after " + g.DOCTYPE_DECL_START + " at position " + p.getIndex());
            }
            var doctype = {
              name: void 0,
              publicId: void 0,
              systemId: void 0,
              internalSubset: void 0
            };
            doctype.name = p.getMatch(g.Name);
            if (!doctype.name)
              return errorHandler.fatalError("doctype name missing or contains unexpected characters at position " + p.getIndex());
            if (isHTML && doctype.name.toLowerCase() !== "html") {
              errorHandler.warning("Unexpected DOCTYPE in HTML document at position " + p.getIndex());
            }
            p.skipBlanks();
            if (p.substringStartsWith(g.PUBLIC) || p.substringStartsWith(g.SYSTEM)) {
              var match = g.ExternalID_match.exec(p.substringFromIndex());
              if (!match) {
                return errorHandler.fatalError("doctype external id is not well-formed at position " + p.getIndex());
              }
              if (match.groups.SystemLiteralOnly !== void 0) {
                doctype.systemId = match.groups.SystemLiteralOnly;
              } else {
                doctype.systemId = match.groups.SystemLiteral;
                doctype.publicId = match.groups.PubidLiteral;
              }
              p.skip(match[0].length);
            } else if (isHTML && p.substringStartsWithCaseInsensitive(g.SYSTEM)) {
              p.skip(g.SYSTEM.length);
              if (p.skipBlanks() < 1) {
                return errorHandler.fatalError("Expected whitespace after " + g.SYSTEM + " at position " + p.getIndex());
              }
              doctype.systemId = p.getMatch(g.ABOUT_LEGACY_COMPAT_SystemLiteral);
              if (!doctype.systemId) {
                return errorHandler.fatalError(
                  "Expected " + g.ABOUT_LEGACY_COMPAT + " in single or double quotes after " + g.SYSTEM + " at position " + p.getIndex()
                );
              }
            }
            if (isHTML && doctype.systemId && !g.ABOUT_LEGACY_COMPAT_SystemLiteral.test(doctype.systemId)) {
              errorHandler.warning("Unexpected doctype.systemId in HTML document at position " + p.getIndex());
            }
            if (!isHTML) {
              p.skipBlanks();
              doctype.internalSubset = parseDoctypeInternalSubset(p, errorHandler);
            }
            p.skipBlanks();
            if (p.char() !== ">") {
              return errorHandler.fatalError("doctype not terminated with > at position " + p.getIndex());
            }
            p.skip(1);
            domBuilder.startDTD(doctype.name, doctype.publicId, doctype.systemId, doctype.internalSubset);
            domBuilder.endDTD();
            return p.getIndex();
          }
          default:
            return errorHandler.fatalError('Not well-formed XML starting with "<!" at position ' + start);
        }
      }
      function parseProcessingInstruction(source, start, domBuilder, errorHandler) {
        var match = source.substring(start).match(g.PI);
        if (!match) {
          return errorHandler.fatalError("Invalid processing instruction starting at position " + start);
        }
        if (match[1].toLowerCase() === "xml") {
          if (start > 0) {
            return errorHandler.fatalError(
              "processing instruction at position " + start + " is an xml declaration which is only at the start of the document"
            );
          }
          if (!g.XMLDecl.test(source.substring(start))) {
            return errorHandler.fatalError("xml declaration is not well-formed");
          }
        }
        domBuilder.processingInstruction(match[1], match[2]);
        return start + match[0].length;
      }
      function ElementAttributes() {
        this.attributeNames = /* @__PURE__ */ Object.create(null);
      }
      ElementAttributes.prototype = {
        setTagName: function(tagName) {
          if (!g.QName_exact.test(tagName)) {
            throw new Error("invalid tagName:" + tagName);
          }
          this.tagName = tagName;
        },
        addValue: function(qName, value, offset) {
          if (!g.QName_exact.test(qName)) {
            throw new Error("invalid attribute:" + qName);
          }
          this.attributeNames[qName] = this.length;
          this[this.length++] = { qName, value, offset };
        },
        length: 0,
        getLocalName: function(i) {
          return this[i].localName;
        },
        getLocator: function(i) {
          return this[i].locator;
        },
        getQName: function(i) {
          return this[i].qName;
        },
        getURI: function(i) {
          return this[i].uri;
        },
        getValue: function(i) {
          return this[i].value;
        }
        //	,getIndex:function(uri, localName)){
        //		if(localName){
        //
        //		}else{
        //			var qName = uri
        //		}
        //	},
        //	getValue:function(){return this.getValue(this.getIndex.apply(this,arguments))},
        //	getType:function(uri,localName){}
        //	getType:function(i){},
      };
      exports.XMLReader = XMLReader;
      exports.parseUtils = parseUtils;
      exports.parseDoctypeCommentOrCData = parseDoctypeCommentOrCData;
    }
  });

  // node_modules/@xmldom/xmldom/lib/dom-parser.js
  var require_dom_parser = __commonJS({
    "node_modules/@xmldom/xmldom/lib/dom-parser.js"(exports) {
      "use strict";
      var conventions = require_conventions();
      var dom = require_dom();
      var errors = require_errors();
      var entities = require_entities();
      var sax = require_sax();
      var DOMImplementation = dom.DOMImplementation;
      var hasDefaultHTMLNamespace = conventions.hasDefaultHTMLNamespace;
      var isHTMLMimeType = conventions.isHTMLMimeType;
      var isValidMimeType = conventions.isValidMimeType;
      var MIME_TYPE = conventions.MIME_TYPE;
      var NAMESPACE = conventions.NAMESPACE;
      var ParseError = errors.ParseError;
      var XMLReader = sax.XMLReader;
      function normalizeLineEndings(input) {
        return input.replace(/\r[\n\u0085]/g, "\n").replace(/[\r\u0085\u2028\u2029]/g, "\n");
      }
      function DOMParser3(options) {
        options = options || {};
        if (options.locator === void 0) {
          options.locator = true;
        }
        this.assign = options.assign || conventions.assign;
        this.domHandler = options.domHandler || DOMHandler;
        this.onError = options.onError || options.errorHandler;
        if (options.errorHandler && typeof options.errorHandler !== "function") {
          throw new TypeError("errorHandler object is no longer supported, switch to onError!");
        } else if (options.errorHandler) {
          options.errorHandler("warning", "The `errorHandler` option has been deprecated, use `onError` instead!", this);
        }
        this.normalizeLineEndings = options.normalizeLineEndings || normalizeLineEndings;
        this.locator = !!options.locator;
        this.xmlns = this.assign(/* @__PURE__ */ Object.create(null), options.xmlns);
      }
      DOMParser3.prototype.parseFromString = function(source, mimeType) {
        if (!isValidMimeType(mimeType)) {
          throw new TypeError('DOMParser.parseFromString: the provided mimeType "' + mimeType + '" is not valid.');
        }
        var defaultNSMap = this.assign(/* @__PURE__ */ Object.create(null), this.xmlns);
        var entityMap = entities.XML_ENTITIES;
        var defaultNamespace = defaultNSMap[""] || null;
        if (hasDefaultHTMLNamespace(mimeType)) {
          entityMap = entities.HTML_ENTITIES;
          defaultNamespace = NAMESPACE.HTML;
        } else if (mimeType === MIME_TYPE.XML_SVG_IMAGE) {
          defaultNamespace = NAMESPACE.SVG;
        }
        defaultNSMap[""] = defaultNamespace;
        defaultNSMap.xml = defaultNSMap.xml || NAMESPACE.XML;
        var domBuilder = new this.domHandler({
          mimeType,
          defaultNamespace,
          onError: this.onError
        });
        var locator = this.locator ? {} : void 0;
        if (this.locator) {
          domBuilder.setDocumentLocator(locator);
        }
        var sax2 = new XMLReader();
        sax2.errorHandler = domBuilder;
        sax2.domBuilder = domBuilder;
        var isXml = !conventions.isHTMLMimeType(mimeType);
        if (isXml && typeof source !== "string") {
          sax2.errorHandler.fatalError("source is not a string");
        }
        sax2.parse(this.normalizeLineEndings(String(source)), defaultNSMap, entityMap);
        if (!domBuilder.doc.documentElement) {
          sax2.errorHandler.fatalError("missing root element");
        }
        return domBuilder.doc;
      };
      function DOMHandler(options) {
        var opt = options || {};
        this.mimeType = opt.mimeType || MIME_TYPE.XML_APPLICATION;
        this.defaultNamespace = opt.defaultNamespace || null;
        this.cdata = false;
        this.currentElement = void 0;
        this.doc = void 0;
        this.locator = void 0;
        this.onError = opt.onError;
      }
      function position(locator, node) {
        node.lineNumber = locator.lineNumber;
        node.columnNumber = locator.columnNumber;
      }
      DOMHandler.prototype = {
        /**
         * Either creates an XML or an HTML document and stores it under `this.doc`.
         * If it is an XML document, `this.defaultNamespace` is used to create it,
         * and it will not contain any `childNodes`.
         * If it is an HTML document, it will be created without any `childNodes`.
         *
         * @see http://www.saxproject.org/apidoc/org/xml/sax/ContentHandler.html
         */
        startDocument: function() {
          var impl = new DOMImplementation();
          this.doc = isHTMLMimeType(this.mimeType) ? impl.createHTMLDocument(false) : impl.createDocument(this.defaultNamespace, "");
        },
        startElement: function(namespaceURI, localName, qName, attrs) {
          var doc = this.doc;
          var el = doc.createElementNS(namespaceURI, qName || localName);
          var len = attrs.length;
          appendElement(this, el);
          this.currentElement = el;
          this.locator && position(this.locator, el);
          for (var i = 0; i < len; i++) {
            var namespaceURI = attrs.getURI(i);
            var value = attrs.getValue(i);
            var qName = attrs.getQName(i);
            var attr = doc.createAttributeNS(namespaceURI, qName);
            this.locator && position(attrs.getLocator(i), attr);
            attr.value = attr.nodeValue = value;
            el.setAttributeNode(attr);
          }
        },
        endElement: function(namespaceURI, localName, qName) {
          this.currentElement = this.currentElement.parentNode;
        },
        startPrefixMapping: function(prefix, uri) {
        },
        endPrefixMapping: function(prefix) {
        },
        processingInstruction: function(target, data) {
          var ins = this.doc.createProcessingInstruction(target, data);
          this.locator && position(this.locator, ins);
          appendElement(this, ins);
        },
        ignorableWhitespace: function(ch3, start, length) {
        },
        characters: function(chars, start, length) {
          chars = _toString.apply(this, arguments);
          if (chars) {
            if (this.cdata) {
              var charNode = this.doc.createCDATASection(chars);
            } else {
              var charNode = this.doc.createTextNode(chars);
            }
            if (this.currentElement) {
              this.currentElement.appendChild(charNode);
            } else if (/^\s*$/.test(chars)) {
              this.doc.appendChild(charNode);
            }
            this.locator && position(this.locator, charNode);
          }
        },
        skippedEntity: function(name) {
        },
        endDocument: function() {
          this.doc.normalize();
        },
        /**
         * Stores the locator to be able to set the `columnNumber` and `lineNumber`
         * on the created DOM nodes.
         *
         * @param {Locator} locator
         */
        setDocumentLocator: function(locator) {
          if (locator) {
            locator.lineNumber = 0;
          }
          this.locator = locator;
        },
        //LexicalHandler
        comment: function(chars, start, length) {
          chars = _toString.apply(this, arguments);
          var comm = this.doc.createComment(chars);
          this.locator && position(this.locator, comm);
          appendElement(this, comm);
        },
        startCDATA: function() {
          this.cdata = true;
        },
        endCDATA: function() {
          this.cdata = false;
        },
        startDTD: function(name, publicId, systemId, internalSubset) {
          var impl = this.doc.implementation;
          if (impl && impl.createDocumentType) {
            var dt = impl.createDocumentType(name, publicId, systemId, internalSubset);
            this.locator && position(this.locator, dt);
            appendElement(this, dt);
            this.doc.doctype = dt;
          }
        },
        reportError: function(level, message) {
          if (typeof this.onError === "function") {
            try {
              this.onError(level, message, this);
            } catch (e) {
              throw new ParseError("Reporting " + level + ' "' + message + '" caused ' + e, this.locator);
            }
          } else {
            console.error("[xmldom " + level + "]	" + message, _locator(this.locator));
          }
        },
        /**
         * @see http://www.saxproject.org/apidoc/org/xml/sax/ErrorHandler.html
         */
        warning: function(message) {
          this.reportError("warning", message);
        },
        error: function(message) {
          this.reportError("error", message);
        },
        /**
         * This function reports a fatal error and throws a ParseError.
         *
         * @param {string} message
         * - The message to be used for reporting and throwing the error.
         * @param {Error} [cause]
         * The error that caused this fatal error, preserved as the thrown `ParseError`'s `cause`.
         * @returns {never}
         * This function always throws an error and never returns a value.
         * @throws {ParseError}
         * Always throws a ParseError with the provided message.
         */
        fatalError: function(message, cause) {
          this.reportError("fatalError", message);
          throw new ParseError(message, this.locator, cause);
        }
      };
      function _locator(l) {
        if (l) {
          return "\n@#[line:" + l.lineNumber + ",col:" + l.columnNumber + "]";
        }
      }
      function _toString(chars, start, length) {
        if (typeof chars == "string") {
          return chars.substr(start, length);
        } else {
          if (chars.length >= start + length || start) {
            return new java.lang.String(chars, start, length) + "";
          }
          return chars;
        }
      }
      "endDTD,startEntity,endEntity,attributeDecl,elementDecl,externalEntityDecl,internalEntityDecl,resolveEntity,getExternalSubset,notationDecl,unparsedEntityDecl".replace(
        /\w+/g,
        function(key2) {
          DOMHandler.prototype[key2] = function() {
            return null;
          };
        }
      );
      function appendElement(handler, node) {
        if (!handler.currentElement) {
          handler.doc.appendChild(node);
        } else {
          handler.currentElement.appendChild(node);
        }
      }
      function onErrorStopParsing(level) {
        if (level === "error") throw "onErrorStopParsing";
      }
      function onWarningStopParsing() {
        throw "onWarningStopParsing";
      }
      exports.__DOMHandler = DOMHandler;
      exports.DOMParser = DOMParser3;
      exports.normalizeLineEndings = normalizeLineEndings;
      exports.onErrorStopParsing = onErrorStopParsing;
      exports.onWarningStopParsing = onWarningStopParsing;
    }
  });

  // node_modules/@xmldom/xmldom/lib/index.js
  var require_lib = __commonJS({
    "node_modules/@xmldom/xmldom/lib/index.js"(exports) {
      "use strict";
      var conventions = require_conventions();
      exports.assign = conventions.assign;
      exports.hasDefaultHTMLNamespace = conventions.hasDefaultHTMLNamespace;
      exports.isHTMLMimeType = conventions.isHTMLMimeType;
      exports.isValidMimeType = conventions.isValidMimeType;
      exports.MIME_TYPE = conventions.MIME_TYPE;
      exports.NAMESPACE = conventions.NAMESPACE;
      var errors = require_errors();
      exports.DOMException = errors.DOMException;
      exports.DOMExceptionName = errors.DOMExceptionName;
      exports.ExceptionCode = errors.ExceptionCode;
      exports.ParseError = errors.ParseError;
      var dom = require_dom();
      exports.Attr = dom.Attr;
      exports.CDATASection = dom.CDATASection;
      exports.CharacterData = dom.CharacterData;
      exports.Comment = dom.Comment;
      exports.Document = dom.Document;
      exports.DocumentFragment = dom.DocumentFragment;
      exports.DocumentType = dom.DocumentType;
      exports.DOMImplementation = dom.DOMImplementation;
      exports.Element = dom.Element;
      exports.Entity = dom.Entity;
      exports.EntityReference = dom.EntityReference;
      exports.LiveNodeList = dom.LiveNodeList;
      exports.NamedNodeMap = dom.NamedNodeMap;
      exports.Node = dom.Node;
      exports.NodeList = dom.NodeList;
      exports.Notation = dom.Notation;
      exports.ProcessingInstruction = dom.ProcessingInstruction;
      exports.Text = dom.Text;
      exports.XMLSerializer = dom.XMLSerializer;
      var domParser = require_dom_parser();
      exports.DOMParser = domParser.DOMParser;
      exports.normalizeLineEndings = domParser.normalizeLineEndings;
      exports.onErrorStopParsing = domParser.onErrorStopParsing;
      exports.onWarningStopParsing = domParser.onWarningStopParsing;
    }
  });

  // node_modules/fflate/esm/browser.js
  var ch2 = {};
  var wk = function(c, id, msg, transfer, cb) {
    var w = new Worker(ch2[id] || (ch2[id] = URL.createObjectURL(new Blob([
      c + ';addEventListener("error",function(e){e=e.error;postMessage({$e$:[e.message,e.code,e.stack]})})'
    ], { type: "text/javascript" }))));
    w.onmessage = function(e) {
      var d = e.data, ed = d.$e$;
      if (ed) {
        var err2 = new Error(ed[0]);
        err2["code"] = ed[1];
        err2.stack = ed[2];
        cb(err2, null);
      } else
        cb(null, d);
    };
    w.postMessage(msg, transfer);
    return w;
  };
  var u8 = Uint8Array;
  var u16 = Uint16Array;
  var i32 = Int32Array;
  var fleb = new u8([
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    1,
    1,
    1,
    1,
    2,
    2,
    2,
    2,
    3,
    3,
    3,
    3,
    4,
    4,
    4,
    4,
    5,
    5,
    5,
    5,
    0,
    /* unused */
    0,
    0,
    /* impossible */
    0
  ]);
  var fdeb = new u8([
    0,
    0,
    0,
    0,
    1,
    1,
    2,
    2,
    3,
    3,
    4,
    4,
    5,
    5,
    6,
    6,
    7,
    7,
    8,
    8,
    9,
    9,
    10,
    10,
    11,
    11,
    12,
    12,
    13,
    13,
    /* unused */
    0,
    0
  ]);
  var clim = new u8([16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15]);
  var freb = function(eb, start) {
    var b = new u16(31);
    for (var i = 0; i < 31; ++i) {
      b[i] = start += 1 << eb[i - 1];
    }
    var r = new i32(b[30]);
    for (var i = 1; i < 30; ++i) {
      for (var j = b[i]; j < b[i + 1]; ++j) {
        r[j] = j - b[i] << 5 | i;
      }
    }
    return { b, r };
  };
  var _a = freb(fleb, 2);
  var fl = _a.b;
  var revfl = _a.r;
  fl[28] = 258, revfl[258] = 28;
  var _b = freb(fdeb, 0);
  var fd = _b.b;
  var revfd = _b.r;
  var rev = new u16(32768);
  for (i = 0; i < 32768; ++i) {
    x = (i & 43690) >> 1 | (i & 21845) << 1;
    x = (x & 52428) >> 2 | (x & 13107) << 2;
    x = (x & 61680) >> 4 | (x & 3855) << 4;
    rev[i] = ((x & 65280) >> 8 | (x & 255) << 8) >> 1;
  }
  var x;
  var i;
  var hMap = function(cd, mb, r) {
    var s = cd.length;
    var i = 0;
    var l = new u16(mb);
    for (; i < s; ++i) {
      if (cd[i])
        ++l[cd[i] - 1];
    }
    var le = new u16(mb);
    for (i = 1; i < mb; ++i) {
      le[i] = le[i - 1] + l[i - 1] << 1;
    }
    var co;
    if (r) {
      co = new u16(1 << mb);
      var rvb = 15 - mb;
      for (i = 0; i < s; ++i) {
        if (cd[i]) {
          var sv = i << 4 | cd[i];
          var r_1 = mb - cd[i];
          var v = le[cd[i] - 1]++ << r_1;
          for (var m = v | (1 << r_1) - 1; v <= m; ++v) {
            co[rev[v] >> rvb] = sv;
          }
        }
      }
    } else {
      co = new u16(s);
      for (i = 0; i < s; ++i) {
        if (cd[i]) {
          co[i] = rev[le[cd[i] - 1]++] >> 15 - cd[i];
        }
      }
    }
    return co;
  };
  var flt = new u8(288);
  for (i = 0; i < 144; ++i)
    flt[i] = 8;
  var i;
  for (i = 144; i < 256; ++i)
    flt[i] = 9;
  var i;
  for (i = 256; i < 280; ++i)
    flt[i] = 7;
  var i;
  for (i = 280; i < 288; ++i)
    flt[i] = 8;
  var i;
  var fdt = new u8(32);
  for (i = 0; i < 32; ++i)
    fdt[i] = 5;
  var i;
  var flm = /* @__PURE__ */ hMap(flt, 9, 0);
  var flrm = /* @__PURE__ */ hMap(flt, 9, 1);
  var fdm = /* @__PURE__ */ hMap(fdt, 5, 0);
  var fdrm = /* @__PURE__ */ hMap(fdt, 5, 1);
  var max = function(a) {
    var m = a[0];
    for (var i = 1; i < a.length; ++i) {
      if (a[i] > m)
        m = a[i];
    }
    return m;
  };
  var bits = function(d, p, m) {
    var o = p / 8 | 0;
    return (d[o] | d[o + 1] << 8) >> (p & 7) & m;
  };
  var bits16 = function(d, p) {
    var o = p / 8 | 0;
    return (d[o] | d[o + 1] << 8 | d[o + 2] << 16) >> (p & 7);
  };
  var shft = function(p) {
    return (p + 7) / 8 | 0;
  };
  var slc = function(v, s, e) {
    if (s == null || s < 0)
      s = 0;
    if (e == null || e > v.length)
      e = v.length;
    return new u8(v.subarray(s, e));
  };
  var ec = [
    "unexpected EOF",
    "invalid block type",
    "invalid length/literal",
    "invalid distance",
    "stream finished",
    "no stream handler",
    ,
    // determined by compression function
    "no callback",
    "invalid UTF-8 data",
    "extra field too long",
    "date not in range 1980-2099",
    "filename too long",
    "stream finishing",
    "invalid zip data"
    // determined by unknown compression method
  ];
  var err = function(ind, msg, nt) {
    var e = new Error(msg || ec[ind]);
    e.code = ind;
    if (Error.captureStackTrace)
      Error.captureStackTrace(e, err);
    if (!nt)
      throw e;
    return e;
  };
  var inflt = function(dat, st, buf, dict) {
    var sl = dat.length, dl = dict ? dict.length : 0;
    if (!sl || st.f && !st.l)
      return buf || new u8(0);
    var noBuf = !buf;
    var resize = noBuf || st.i != 2;
    var noSt = st.i;
    if (noBuf)
      buf = new u8(sl * 3);
    var cbuf = function(l2) {
      var bl = buf.length;
      if (l2 > bl) {
        var nbuf = new u8(Math.max(bl * 2, l2));
        nbuf.set(buf);
        buf = nbuf;
      }
    };
    var final = st.f || 0, pos = st.p || 0, bt = st.b || 0, lm = st.l, dm = st.d, lbt = st.m, dbt = st.n;
    var tbts = sl * 8;
    do {
      if (!lm) {
        final = bits(dat, pos, 1);
        var type = bits(dat, pos + 1, 3);
        pos += 3;
        if (!type) {
          var s = shft(pos) + 4, l = dat[s - 4] | dat[s - 3] << 8, t = s + l;
          if (t > sl) {
            if (noSt)
              err(0);
            break;
          }
          if (resize)
            cbuf(bt + l);
          buf.set(dat.subarray(s, t), bt);
          st.b = bt += l, st.p = pos = t * 8, st.f = final;
          continue;
        } else if (type == 1)
          lm = flrm, dm = fdrm, lbt = 9, dbt = 5;
        else if (type == 2) {
          var hLit = bits(dat, pos, 31) + 257, hcLen = bits(dat, pos + 10, 15) + 4;
          var tl = hLit + bits(dat, pos + 5, 31) + 1;
          pos += 14;
          var ldt = new u8(tl);
          var clt = new u8(19);
          for (var i = 0; i < hcLen; ++i) {
            clt[clim[i]] = bits(dat, pos + i * 3, 7);
          }
          pos += hcLen * 3;
          var clb = max(clt), clbmsk = (1 << clb) - 1;
          var clm = hMap(clt, clb, 1);
          for (var i = 0; i < tl; ) {
            var r = clm[bits(dat, pos, clbmsk)];
            pos += r & 15;
            var s = r >> 4;
            if (s < 16) {
              ldt[i++] = s;
            } else {
              var c = 0, n = 0;
              if (s == 16)
                n = 3 + bits(dat, pos, 3), pos += 2, c = ldt[i - 1];
              else if (s == 17)
                n = 3 + bits(dat, pos, 7), pos += 3;
              else if (s == 18)
                n = 11 + bits(dat, pos, 127), pos += 7;
              while (n--)
                ldt[i++] = c;
            }
          }
          var lt = ldt.subarray(0, hLit), dt = ldt.subarray(hLit);
          lbt = max(lt);
          dbt = max(dt);
          lm = hMap(lt, lbt, 1);
          dm = hMap(dt, dbt, 1);
        } else
          err(1);
        if (pos > tbts) {
          if (noSt)
            err(0);
          break;
        }
      }
      if (resize)
        cbuf(bt + 131072);
      var lms = (1 << lbt) - 1, dms = (1 << dbt) - 1;
      var lpos = pos;
      for (; ; lpos = pos) {
        var c = lm[bits16(dat, pos) & lms], sym = c >> 4;
        pos += c & 15;
        if (pos > tbts) {
          if (noSt)
            err(0);
          break;
        }
        if (!c)
          err(2);
        if (sym < 256)
          buf[bt++] = sym;
        else if (sym == 256) {
          lpos = pos, lm = null;
          break;
        } else {
          var add = sym - 254;
          if (sym > 264) {
            var i = sym - 257, b = fleb[i];
            add = bits(dat, pos, (1 << b) - 1) + fl[i];
            pos += b;
          }
          var d = dm[bits16(dat, pos) & dms], dsym = d >> 4;
          if (!d)
            err(3);
          pos += d & 15;
          var dt = fd[dsym];
          if (dsym > 3) {
            var b = fdeb[dsym];
            dt += bits16(dat, pos) & (1 << b) - 1, pos += b;
          }
          if (pos > tbts) {
            if (noSt)
              err(0);
            break;
          }
          if (resize)
            cbuf(bt + 131072);
          var end = bt + add;
          if (bt < dt) {
            var shift = dl - dt, dend = Math.min(dt, end);
            if (shift + bt < 0)
              err(3);
            for (; bt < dend; ++bt)
              buf[bt] = dict[shift + bt];
          }
          for (; bt < end; ++bt)
            buf[bt] = buf[bt - dt];
        }
      }
      st.l = lm, st.p = lpos, st.b = bt, st.f = final;
      if (lm)
        final = 1, st.m = lbt, st.d = dm, st.n = dbt;
    } while (!final);
    return bt != buf.length && noBuf ? slc(buf, 0, bt) : buf.subarray(0, bt);
  };
  var wbits = function(d, p, v) {
    v <<= p & 7;
    var o = p / 8 | 0;
    d[o] |= v;
    d[o + 1] |= v >> 8;
  };
  var wbits16 = function(d, p, v) {
    v <<= p & 7;
    var o = p / 8 | 0;
    d[o] |= v;
    d[o + 1] |= v >> 8;
    d[o + 2] |= v >> 16;
  };
  var hTree = function(d, mb) {
    var t = [];
    for (var i = 0; i < d.length; ++i) {
      if (d[i])
        t.push({ s: i, f: d[i] });
    }
    var s = t.length;
    var t2 = t.slice();
    if (!s)
      return { t: et, l: 0 };
    if (s == 1) {
      var v = new u8(t[0].s + 1);
      v[t[0].s] = 1;
      return { t: v, l: 1 };
    }
    t.sort(function(a, b) {
      return a.f - b.f;
    });
    t.push({ s: -1, f: 25001 });
    var l = t[0], r = t[1], i0 = 0, i1 = 1, i2 = 2;
    t[0] = { s: -1, f: l.f + r.f, l, r };
    while (i1 != s - 1) {
      l = t[t[i0].f < t[i2].f ? i0++ : i2++];
      r = t[i0 != i1 && t[i0].f < t[i2].f ? i0++ : i2++];
      t[i1++] = { s: -1, f: l.f + r.f, l, r };
    }
    var maxSym = t2[0].s;
    for (var i = 1; i < s; ++i) {
      if (t2[i].s > maxSym)
        maxSym = t2[i].s;
    }
    var tr = new u16(maxSym + 1);
    var mbt = ln(t[i1 - 1], tr, 0);
    if (mbt > mb) {
      var i = 0, dt = 0;
      var lft = mbt - mb, cst = 1 << lft;
      t2.sort(function(a, b) {
        return tr[b.s] - tr[a.s] || a.f - b.f;
      });
      for (; i < s; ++i) {
        var i2_1 = t2[i].s;
        if (tr[i2_1] > mb) {
          dt += cst - (1 << mbt - tr[i2_1]);
          tr[i2_1] = mb;
        } else
          break;
      }
      dt >>= lft;
      while (dt > 0) {
        var i2_2 = t2[i].s;
        if (tr[i2_2] < mb)
          dt -= 1 << mb - tr[i2_2]++ - 1;
        else
          ++i;
      }
      for (; i >= 0 && dt; --i) {
        var i2_3 = t2[i].s;
        if (tr[i2_3] == mb) {
          --tr[i2_3];
          ++dt;
        }
      }
      mbt = mb;
    }
    return { t: new u8(tr), l: mbt };
  };
  var ln = function(n, l, d) {
    return n.s == -1 ? Math.max(ln(n.l, l, d + 1), ln(n.r, l, d + 1)) : l[n.s] = d;
  };
  var lc = function(c) {
    var s = c.length;
    while (s && !c[--s])
      ;
    var cl = new u16(++s);
    var cli = 0, cln = c[0], cls = 1;
    var w = function(v) {
      cl[cli++] = v;
    };
    for (var i = 1; i <= s; ++i) {
      if (c[i] == cln && i != s)
        ++cls;
      else {
        if (!cln && cls > 2) {
          for (; cls > 138; cls -= 138)
            w(32754);
          if (cls > 2) {
            w(cls > 10 ? cls - 11 << 5 | 28690 : cls - 3 << 5 | 12305);
            cls = 0;
          }
        } else if (cls > 3) {
          w(cln), --cls;
          for (; cls > 6; cls -= 6)
            w(8304);
          if (cls > 2)
            w(cls - 3 << 5 | 8208), cls = 0;
        }
        while (cls--)
          w(cln);
        cls = 1;
        cln = c[i];
      }
    }
    return { c: cl.subarray(0, cli), n: s };
  };
  var clen = function(cf, cl) {
    var l = 0;
    for (var i = 0; i < cl.length; ++i)
      l += cf[i] * cl[i];
    return l;
  };
  var wfblk = function(out, pos, dat) {
    var s = dat.length;
    var o = shft(pos + 2);
    out[o] = s & 255;
    out[o + 1] = s >> 8;
    out[o + 2] = out[o] ^ 255;
    out[o + 3] = out[o + 1] ^ 255;
    for (var i = 0; i < s; ++i)
      out[o + i + 4] = dat[i];
    return (o + 4 + s) * 8;
  };
  var wblk = function(dat, out, final, syms, lf, df, eb, li, bs, bl, p) {
    wbits(out, p++, final);
    ++lf[256];
    var _a2 = hTree(lf, 15), dlt = _a2.t, mlb = _a2.l;
    var _b2 = hTree(df, 15), ddt = _b2.t, mdb = _b2.l;
    var _c = lc(dlt), lclt = _c.c, nlc = _c.n;
    var _d = lc(ddt), lcdt = _d.c, ndc = _d.n;
    var lcfreq = new u16(19);
    for (var i = 0; i < lclt.length; ++i)
      ++lcfreq[lclt[i] & 31];
    for (var i = 0; i < lcdt.length; ++i)
      ++lcfreq[lcdt[i] & 31];
    var _e = hTree(lcfreq, 7), lct = _e.t, mlcb = _e.l;
    var nlcc = 19;
    for (; nlcc > 4 && !lct[clim[nlcc - 1]]; --nlcc)
      ;
    var flen = bl + 5 << 3;
    var ftlen = clen(lf, flt) + clen(df, fdt) + eb;
    var dtlen = clen(lf, dlt) + clen(df, ddt) + eb + 14 + 3 * nlcc + clen(lcfreq, lct) + 2 * lcfreq[16] + 3 * lcfreq[17] + 7 * lcfreq[18];
    if (bs >= 0 && flen <= ftlen && flen <= dtlen)
      return wfblk(out, p, dat.subarray(bs, bs + bl));
    var lm, ll, dm, dl;
    wbits(out, p, 1 + (dtlen < ftlen)), p += 2;
    if (dtlen < ftlen) {
      lm = hMap(dlt, mlb, 0), ll = dlt, dm = hMap(ddt, mdb, 0), dl = ddt;
      var llm = hMap(lct, mlcb, 0);
      wbits(out, p, nlc - 257);
      wbits(out, p + 5, ndc - 1);
      wbits(out, p + 10, nlcc - 4);
      p += 14;
      for (var i = 0; i < nlcc; ++i)
        wbits(out, p + 3 * i, lct[clim[i]]);
      p += 3 * nlcc;
      var lcts = [lclt, lcdt];
      for (var it = 0; it < 2; ++it) {
        var clct = lcts[it];
        for (var i = 0; i < clct.length; ++i) {
          var len = clct[i] & 31;
          wbits(out, p, llm[len]), p += lct[len];
          if (len > 15)
            wbits(out, p, clct[i] >> 5 & 127), p += clct[i] >> 12;
        }
      }
    } else {
      lm = flm, ll = flt, dm = fdm, dl = fdt;
    }
    for (var i = 0; i < li; ++i) {
      var sym = syms[i];
      if (sym > 255) {
        var len = sym >> 18 & 31;
        wbits16(out, p, lm[len + 257]), p += ll[len + 257];
        if (len > 7)
          wbits(out, p, sym >> 23 & 31), p += fleb[len];
        var dst = sym & 31;
        wbits16(out, p, dm[dst]), p += dl[dst];
        if (dst > 3)
          wbits16(out, p, sym >> 5 & 8191), p += fdeb[dst];
      } else {
        wbits16(out, p, lm[sym]), p += ll[sym];
      }
    }
    wbits16(out, p, lm[256]);
    return p + ll[256];
  };
  var deo = /* @__PURE__ */ new i32([65540, 131080, 131088, 131104, 262176, 1048704, 1048832, 2114560, 2117632]);
  var et = /* @__PURE__ */ new u8(0);
  var dflt = function(dat, lvl, plvl, pre, post, st) {
    var s = st.z || dat.length;
    var o = new u8(pre + s + 5 * (1 + Math.ceil(s / 7e3)) + post);
    var w = o.subarray(pre, o.length - post);
    var lst = st.l;
    var pos = (st.r || 0) & 7;
    if (lvl) {
      if (pos)
        w[0] = st.r >> 3;
      var opt = deo[lvl - 1];
      var n = opt >> 13, c = opt & 8191;
      var msk_1 = (1 << plvl) - 1;
      var prev = st.p || new u16(32768), head = st.h || new u16(msk_1 + 1);
      var bs1_1 = Math.ceil(plvl / 3), bs2_1 = 2 * bs1_1;
      var hsh = function(i2) {
        return (dat[i2] ^ dat[i2 + 1] << bs1_1 ^ dat[i2 + 2] << bs2_1) & msk_1;
      };
      var syms = new i32(25e3);
      var lf = new u16(288), df = new u16(32);
      var lc_1 = 0, eb = 0, i = st.i || 0, li = 0, wi = st.w || 0, bs = 0;
      for (; i + 2 < s; ++i) {
        var hv = hsh(i);
        var imod = i & 32767, pimod = head[hv];
        prev[imod] = pimod;
        head[hv] = imod;
        if (wi <= i) {
          var rem = s - i;
          if ((lc_1 > 7e3 || li > 24576) && (rem > 423 || !lst)) {
            pos = wblk(dat, w, 0, syms, lf, df, eb, li, bs, i - bs, pos);
            li = lc_1 = eb = 0, bs = i;
            for (var j = 0; j < 286; ++j)
              lf[j] = 0;
            for (var j = 0; j < 30; ++j)
              df[j] = 0;
          }
          var l = 2, d = 0, ch_1 = c, dif = imod - pimod & 32767;
          if (rem > 2 && hv == hsh(i - dif)) {
            var maxn = Math.min(n, rem) - 1;
            var maxd = Math.min(32767, i);
            var ml = Math.min(258, rem);
            while (dif <= maxd && --ch_1 && imod != pimod) {
              if (dat[i + l] == dat[i + l - dif]) {
                var nl = 0;
                for (; nl < ml && dat[i + nl] == dat[i + nl - dif]; ++nl)
                  ;
                if (nl > l) {
                  l = nl, d = dif;
                  if (nl > maxn)
                    break;
                  var mmd = Math.min(dif, nl - 2);
                  var md = 0;
                  for (var j = 0; j < mmd; ++j) {
                    var ti = i - dif + j & 32767;
                    var pti = prev[ti];
                    var cd = ti - pti & 32767;
                    if (cd > md)
                      md = cd, pimod = ti;
                  }
                }
              }
              imod = pimod, pimod = prev[imod];
              dif += imod - pimod & 32767;
            }
          }
          if (d) {
            syms[li++] = 268435456 | revfl[l] << 18 | revfd[d];
            var lin = revfl[l] & 31, din = revfd[d] & 31;
            eb += fleb[lin] + fdeb[din];
            ++lf[257 + lin];
            ++df[din];
            wi = i + l;
            ++lc_1;
          } else {
            syms[li++] = dat[i];
            ++lf[dat[i]];
          }
        }
      }
      for (i = Math.max(i, wi); i < s; ++i) {
        syms[li++] = dat[i];
        ++lf[dat[i]];
      }
      pos = wblk(dat, w, lst, syms, lf, df, eb, li, bs, i - bs, pos);
      if (!lst) {
        st.r = pos & 7 | w[pos / 8 | 0] << 3;
        pos -= 7;
        st.h = head, st.p = prev, st.i = i, st.w = wi;
      }
    } else {
      for (var i = st.w || 0; i < s + lst; i += 65535) {
        var e = i + 65535;
        if (e >= s) {
          w[pos / 8 | 0] = lst;
          e = s;
        }
        pos = wfblk(w, pos + 1, dat.subarray(i, e));
      }
      st.i = s;
    }
    return slc(o, 0, pre + shft(pos) + post);
  };
  var crct = /* @__PURE__ */ function() {
    var t = new Int32Array(256);
    for (var i = 0; i < 256; ++i) {
      var c = i, k = 9;
      while (--k)
        c = (c & 1 && -306674912) ^ c >>> 1;
      t[i] = c;
    }
    return t;
  }();
  var crc = function() {
    var c = -1;
    return {
      p: function(d) {
        var cr = c;
        for (var i = 0; i < d.length; ++i)
          cr = crct[cr & 255 ^ d[i]] ^ cr >>> 8;
        c = cr;
      },
      d: function() {
        return ~c;
      }
    };
  };
  var dopt = function(dat, opt, pre, post, st) {
    if (!st) {
      st = { l: 1 };
      if (opt.dictionary) {
        var dict = opt.dictionary.subarray(-32768);
        var newDat = new u8(dict.length + dat.length);
        newDat.set(dict);
        newDat.set(dat, dict.length);
        dat = newDat;
        st.w = dict.length;
      }
    }
    return dflt(dat, opt.level == null ? 6 : opt.level, opt.mem == null ? st.l ? Math.ceil(Math.max(8, Math.min(13, Math.log(dat.length))) * 1.5) : 20 : 12 + opt.mem, pre, post, st);
  };
  var mrg = function(a, b) {
    var o = {};
    for (var k in a)
      o[k] = a[k];
    for (var k in b)
      o[k] = b[k];
    return o;
  };
  var wcln = function(fn, fnStr, td2) {
    var dt = fn();
    var st = fn.toString();
    var ks = st.slice(st.indexOf("[") + 1, st.lastIndexOf("]")).replace(/\s+/g, "").split(",");
    for (var i = 0; i < dt.length; ++i) {
      var v = dt[i], k = ks[i];
      if (typeof v == "function") {
        fnStr += ";" + k + "=";
        var st_1 = v.toString();
        if (v.prototype) {
          if (st_1.indexOf("[native code]") != -1) {
            var spInd = st_1.indexOf(" ", 8) + 1;
            fnStr += st_1.slice(spInd, st_1.indexOf("(", spInd));
          } else {
            fnStr += st_1;
            for (var t in v.prototype)
              fnStr += ";" + k + ".prototype." + t + "=" + v.prototype[t].toString();
          }
        } else
          fnStr += st_1;
      } else
        td2[k] = v;
    }
    return fnStr;
  };
  var ch = [];
  var cbfs = function(v) {
    var tl = [];
    for (var k in v) {
      if (v[k].buffer) {
        tl.push((v[k] = new v[k].constructor(v[k])).buffer);
      }
    }
    return tl;
  };
  var wrkr = function(fns, init, id, cb) {
    if (!ch[id]) {
      var fnStr = "", td_1 = {}, m = fns.length - 1;
      for (var i = 0; i < m; ++i)
        fnStr = wcln(fns[i], fnStr, td_1);
      ch[id] = { c: wcln(fns[m], fnStr, td_1), e: td_1 };
    }
    var td2 = mrg({}, ch[id].e);
    return wk(ch[id].c + ";onmessage=function(e){for(var k in e.data)self[k]=e.data[k];onmessage=" + init.toString() + "}", id, td2, cbfs(td2), cb);
  };
  var bInflt = function() {
    return [u8, u16, i32, fleb, fdeb, clim, fl, fd, flrm, fdrm, rev, ec, hMap, max, bits, bits16, shft, slc, err, inflt, inflateSync, pbf, gopt];
  };
  var pbf = function(msg) {
    return postMessage(msg, [msg.buffer]);
  };
  var gopt = function(o) {
    return o && {
      out: o.size && new u8(o.size),
      dictionary: o.dictionary
    };
  };
  var cbify = function(dat, opts, fns, init, id, cb) {
    var w = wrkr(fns, init, id, function(err2, dat2) {
      w.terminate();
      cb(err2, dat2);
    });
    w.postMessage([dat, opts], opts.consume ? [dat.buffer] : []);
    return function() {
      w.terminate();
    };
  };
  var b2 = function(d, b) {
    return d[b] | d[b + 1] << 8;
  };
  var b4 = function(d, b) {
    return (d[b] | d[b + 1] << 8 | d[b + 2] << 16 | d[b + 3] << 24) >>> 0;
  };
  var b8 = function(d, b) {
    return b4(d, b) + b4(d, b + 4) * 4294967296;
  };
  var wbytes = function(d, b, v) {
    for (; v; ++b)
      d[b] = v, v >>>= 8;
  };
  function deflateSync(data, opts) {
    return dopt(data, opts || {}, 0, 0);
  }
  function inflate(data, opts, cb) {
    if (!cb)
      cb = opts, opts = {};
    if (typeof cb != "function")
      err(7);
    return cbify(data, opts, [
      bInflt
    ], function(ev) {
      return pbf(inflateSync(ev.data[0], gopt(ev.data[1])));
    }, 1, cb);
  }
  function inflateSync(data, opts) {
    return inflt(data, { i: 2 }, opts && opts.out, opts && opts.dictionary);
  }
  var fltn = function(d, p, t, o) {
    for (var k in d) {
      var val = d[k], n = p + k, op = o;
      if (Array.isArray(val))
        op = mrg(o, val[1]), val = val[0];
      if (ArrayBuffer.isView(val))
        t[n] = [val, op];
      else {
        t[n += "/"] = [new u8(0), op];
        fltn(val, n, t, o);
      }
    }
  };
  var te = typeof TextEncoder != "undefined" && /* @__PURE__ */ new TextEncoder();
  var td = typeof TextDecoder != "undefined" && /* @__PURE__ */ new TextDecoder();
  var tds = 0;
  try {
    td.decode(et, { stream: true });
    tds = 1;
  } catch (e) {
  }
  var dutf8 = function(d) {
    for (var r = "", i = 0; ; ) {
      var c = d[i++];
      var eb = (c > 127) + (c > 223) + (c > 239);
      if (i + eb > d.length)
        return { s: r, r: slc(d, i - 1) };
      if (!eb)
        r += String.fromCharCode(c);
      else if (eb == 3) {
        c = ((c & 15) << 18 | (d[i++] & 63) << 12 | (d[i++] & 63) << 6 | d[i++] & 63) - 65536, r += String.fromCharCode(55296 | c >> 10, 56320 | c & 1023);
      } else if (eb & 1)
        r += String.fromCharCode((c & 31) << 6 | d[i++] & 63);
      else
        r += String.fromCharCode((c & 15) << 12 | (d[i++] & 63) << 6 | d[i++] & 63);
    }
  };
  function strToU8(str, latin1) {
    if (latin1) {
      var ar_1 = new u8(str.length);
      for (var i = 0; i < str.length; ++i)
        ar_1[i] = str.charCodeAt(i);
      return ar_1;
    }
    if (te)
      return te.encode(str);
    var l = str.length;
    var ar = new u8(str.length + (str.length >> 1));
    var ai = 0;
    var w = function(v) {
      ar[ai++] = v;
    };
    for (var i = 0; i < l; ++i) {
      if (ai + 5 > ar.length) {
        var n = new u8(ai + 8 + (l - i << 1));
        n.set(ar);
        ar = n;
      }
      var c = str.charCodeAt(i);
      if (c < 128 || latin1)
        w(c);
      else if (c < 2048)
        w(192 | c >> 6), w(128 | c & 63);
      else if (c > 55295 && c < 57344)
        c = 65536 + (c & 1023 << 10) | str.charCodeAt(++i) & 1023, w(240 | c >> 18), w(128 | c >> 12 & 63), w(128 | c >> 6 & 63), w(128 | c & 63);
      else
        w(224 | c >> 12), w(128 | c >> 6 & 63), w(128 | c & 63);
    }
    return slc(ar, 0, ai);
  }
  function strFromU8(dat, latin1) {
    if (latin1) {
      var r = "";
      for (var i = 0; i < dat.length; i += 16384)
        r += String.fromCharCode.apply(null, dat.subarray(i, i + 16384));
      return r;
    } else if (td) {
      return td.decode(dat);
    } else {
      var _a2 = dutf8(dat), s = _a2.s, r = _a2.r;
      if (r.length)
        err(8);
      return s;
    }
  }
  var slzh = function(d, b) {
    return b + 30 + b2(d, b + 26) + b2(d, b + 28);
  };
  var zh = function(d, b, z) {
    var fnl = b2(d, b + 28), efl = b2(d, b + 30), fn = strFromU8(d.subarray(b + 46, b + 46 + fnl), !(b2(d, b + 8) & 2048)), es = b + 46 + fnl;
    var _a2 = z64hs(d, es, efl, z, b4(d, b + 20), b4(d, b + 24), b4(d, b + 42)), sc = _a2[0], su = _a2[1], off = _a2[2];
    return [b2(d, b + 10), sc, su, fn, es + efl + b2(d, b + 32), off];
  };
  var z64hs = function(d, b, l, z, sc, su, off) {
    var nsc = sc == 4294967295, nsu = su == 4294967295, noff = off == 4294967295, e = b + l;
    var nf = nsc + nsu + noff;
    if (z && nf) {
      for (; b + 4 < e; b += 4 + b2(d, b + 2)) {
        if (b2(d, b) == 1) {
          return [
            nsc ? b8(d, b + 4 + 8 * nsu) : sc,
            nsu ? b8(d, b + 4) : su,
            noff ? b8(d, b + 4 + 8 * (nsu + nsc)) : off,
            1
          ];
        }
      }
      if (z < 2)
        err(13);
    }
    return [sc, su, off, 0];
  };
  var exfl = function(ex) {
    var le = 0;
    if (ex) {
      for (var k in ex) {
        var l = ex[k].length;
        if (l > 65535)
          err(9);
        le += l + 4;
      }
    }
    return le;
  };
  var wzh = function(d, b, f, fn, u, c, ce, co) {
    var fl2 = fn.length, ex = f.extra, col = co && co.length;
    var exl = exfl(ex);
    wbytes(d, b, ce != null ? 33639248 : 67324752), b += 4;
    if (ce != null)
      d[b++] = 20, d[b++] = f.os;
    d[b] = 20, b += 2;
    d[b++] = f.flag << 1 | (c < 0 && 8), d[b++] = u && 8;
    d[b++] = f.compression & 255, d[b++] = f.compression >> 8;
    var dt = new Date(f.mtime == null ? Date.now() : f.mtime), y = dt.getFullYear() - 1980;
    if (y < 0 || y > 119)
      err(10);
    wbytes(d, b, y << 25 | dt.getMonth() + 1 << 21 | dt.getDate() << 16 | dt.getHours() << 11 | dt.getMinutes() << 5 | dt.getSeconds() >> 1), b += 4;
    if (c != -1) {
      wbytes(d, b, f.crc);
      wbytes(d, b + 4, c < 0 ? -c - 2 : c);
      wbytes(d, b + 8, f.size);
    }
    wbytes(d, b + 12, fl2);
    wbytes(d, b + 14, exl), b += 16;
    if (ce != null) {
      wbytes(d, b, col);
      wbytes(d, b + 6, f.attrs);
      wbytes(d, b + 10, ce), b += 14;
    }
    d.set(fn, b);
    b += fl2;
    if (exl) {
      for (var k in ex) {
        var exf = ex[k], l = exf.length;
        wbytes(d, b, +k);
        wbytes(d, b + 2, l);
        d.set(exf, b + 4), b += 4 + l;
      }
    }
    if (col)
      d.set(co, b), b += col;
    return b;
  };
  var wzf = function(o, b, c, d, e) {
    wbytes(o, b, 101010256);
    wbytes(o, b + 8, c);
    wbytes(o, b + 10, c);
    wbytes(o, b + 12, d);
    wbytes(o, b + 16, e);
  };
  function zipSync(data, opts) {
    if (!opts)
      opts = {};
    var r = {};
    var files = [];
    fltn(data, "", r, opts);
    var o = 0;
    var tot = 0;
    for (var fn in r) {
      var _a2 = r[fn], file = _a2[0], p = _a2[1];
      var compression = p.level == 0 ? 0 : 8;
      var f = strToU8(fn), s = f.length;
      var com = p.comment, m = com && strToU8(com), ms = m && m.length;
      var exl = exfl(p.extra);
      if (s > 65535)
        err(11);
      var d = compression ? deflateSync(file, p) : file, l = d.length;
      var c = crc();
      c.p(file);
      files.push(mrg(p, {
        size: file.length,
        crc: c.d(),
        c: d,
        f,
        m,
        u: s != fn.length || m && com.length != ms,
        o,
        compression
      }));
      o += 30 + s + exl + l;
      tot += 76 + 2 * (s + exl) + (ms || 0) + l;
    }
    var out = new u8(tot + 22), oe = o, cdl = tot - o;
    for (var i = 0; i < files.length; ++i) {
      var f = files[i];
      wzh(out, f.o, f, f.f, f.u, f.c.length);
      var badd = 30 + f.f.length + exfl(f.extra);
      out.set(f.c, f.o + badd);
      wzh(out, o, f, f.f, f.u, f.c.length, f.o, f.m), o += 16 + badd + (f.m ? f.m.length : 0);
    }
    wzf(out, o, files.length, cdl, oe);
    return out;
  }
  var mt = typeof queueMicrotask == "function" ? queueMicrotask : typeof setTimeout == "function" ? setTimeout : function(fn) {
    fn();
  };
  function unzip(data, opts, cb) {
    if (!cb)
      cb = opts, opts = {};
    if (typeof cb != "function")
      err(7);
    var term = [];
    var tAll = function() {
      for (var i2 = 0; i2 < term.length; ++i2)
        term[i2]();
    };
    var files = {};
    var cbd = function(a, b) {
      mt(function() {
        cb(a, b);
      });
    };
    mt(function() {
      cbd = cb;
    });
    var e = data.length - 22;
    for (; b4(data, e) != 101010256; --e) {
      if (!e || data.length - e > 65558) {
        cbd(err(13, 0, 1), null);
        return tAll;
      }
    }
    ;
    var lft = b2(data, e + 8);
    if (lft) {
      var c = lft;
      var o = b4(data, e + 16);
      var z = b4(data, e - 20) == 117853008;
      if (z) {
        var ze = b4(data, e - 12);
        z = b4(data, ze) == 101075792;
        if (z) {
          c = lft = b4(data, ze + 32);
          o = b4(data, ze + 48);
        }
      }
      var fltr = opts && opts.filter;
      var _loop_3 = function(i2) {
        var _a2 = zh(data, o, z), c_1 = _a2[0], sc = _a2[1], su = _a2[2], fn = _a2[3], no = _a2[4], off = _a2[5], b = slzh(data, off);
        o = no;
        var cbl = function(e2, d) {
          if (e2) {
            tAll();
            cbd(e2, null);
          } else {
            if (d)
              files[fn] = d;
            if (!--lft)
              cbd(null, files);
          }
        };
        if (!fltr || fltr({
          name: fn,
          size: sc,
          originalSize: su,
          compression: c_1
        })) {
          if (!c_1)
            cbl(null, slc(data, b, b + sc));
          else if (c_1 == 8) {
            var infl = data.subarray(b, b + sc);
            if (su < 524288 || sc > 0.8 * su) {
              try {
                cbl(null, inflateSync(infl, { out: new u8(su) }));
              } catch (e2) {
                cbl(e2, null);
              }
            } else
              term.push(inflate(infl, { size: su }, cbl));
          } else
            cbl(err(14, "unknown compression type " + c_1, 1), null);
        } else
          cbl(null, null);
      };
      for (var i = 0; i < c; ++i) {
        _loop_3(i);
      }
    } else
      cbd(null, {});
    return tAll;
  }
  function unzipSync(data, opts) {
    var files = {};
    var e = data.length - 22;
    for (; b4(data, e) != 101010256; --e) {
      if (!e || data.length - e > 65558)
        err(13);
    }
    ;
    var c = b2(data, e + 8);
    if (!c)
      return {};
    var o = b4(data, e + 16);
    var z = b4(data, e - 20) == 117853008;
    if (z) {
      var ze = b4(data, e - 12);
      z = b4(data, ze) == 101075792;
      if (z) {
        c = b4(data, ze + 32);
        o = b4(data, ze + 48);
      }
    }
    var fltr = opts && opts.filter;
    for (var i = 0; i < c; ++i) {
      var _a2 = zh(data, o, z), c_2 = _a2[0], sc = _a2[1], su = _a2[2], fn = _a2[3], no = _a2[4], off = _a2[5], b = slzh(data, off);
      o = no;
      if (!fltr || fltr({
        name: fn,
        size: sc,
        originalSize: su,
        compression: c_2
      })) {
        if (!c_2)
          files[fn] = slc(data, b, b + sc);
        else if (c_2 == 8)
          files[fn] = inflateSync(data.subarray(b, b + sc), { out: new u8(su) });
        else
          err(14, "unknown compression type " + c_2);
      }
    }
    return files;
  }

  // node_modules/worker-f/lib/stringifyFunctionReferences.js
  var JAVASCRIPT_VARIABLE_NAME_REG_EXP = /^[$_\u0080-\uFFFFa-zA-Z][$_\u0080-\uFFFF\w]*$/;
  function stringifyFunctionReferences(getDependencies) {
    const functions = {};
    const variables = {};
    const references = getDependencies();
    const getReferencesSourceCode = getDependencies.toString();
    const referencedNames = getReferencesSourceCode.slice(getReferencesSourceCode.indexOf("[") + 1, getReferencesSourceCode.lastIndexOf("]")).split(",").map((_) => _.trim());
    for (const name of referencedNames) {
      if (!JAVASCRIPT_VARIABLE_NAME_REG_EXP.test(name)) {
        throw new Error(`Invalid dependency name: ${name}`);
      }
    }
    let i = 0;
    while (i < references.length) {
      let name = referencedNames[i];
      let value = references[i];
      if (typeof value === "function") {
        functions[name] = getFunctionSourceCode(value, name);
      } else {
        variables[name] = value;
      }
      i++;
    }
    return {
      functions,
      variables
    };
  }
  function getFunctionSourceCode(func, name) {
    const funcSourceCode = func.toString();
    if (func.prototype) {
      if (funcSourceCode.indexOf("[native code]") >= 0) {
        const funcNameStartsAt = funcSourceCode.indexOf(" ", "function".length) + " ".length;
        const funcNameEndsBefore = funcSourceCode.indexOf("(", funcNameStartsAt);
        return funcSourceCode.slice(funcNameStartsAt, funcNameEndsBefore);
      } else {
        let code = funcSourceCode;
        for (const key2 in func.prototype) {
          code += ";" + name + ".prototype." + key2 + "=" + func.prototype[key2].toString();
        }
        return code;
      }
    } else {
      return funcSourceCode;
    }
  }

  // node_modules/worker-f/lib/createWorker.js
  function createWorker(createWorkerInEnvironment, createInputHandler, onError, onOutput, getFromCache, setInCache) {
    let started = false;
    let worker;
    const stop = () => {
      worker.stop();
    };
    const ingest = (data, transferList) => {
      try {
        worker.ingest(data, transferList);
      } catch (error) {
        stop();
        throw error;
      }
    };
    const start = (arrayOfGetDependenciesFunctions, dependenciesTransferList) => {
      if (started) {
        throw new Error("Was started");
      }
      started = true;
      const cacheValue = getFromCache();
      const cachedCodeAndVars = cacheValue && cacheValue._;
      const codeAndVars = cachedCodeAndVars || getCodeAndVars(createInputHandler, arrayOfGetDependenciesFunctions);
      if (!cachedCodeAndVars) {
        setInCache({ _: codeAndVars });
      }
      const [code, vars] = codeAndVars;
      const getOtherFromCache = () => {
        const properties = getFromCache();
        if (properties) {
          return properties.other;
        }
      };
      const setOtherInCache = (value) => {
        setInCache(Object.assign(Object.assign({}, getFromCache()), { other: value }));
      };
      worker = createWorkerInEnvironment(code, getOtherFromCache, setOtherInCache, onError, onOutput);
      if (vars) {
        ingest(vars, dependenciesTransferList);
      }
    };
    return {
      start,
      stop,
      ingest
    };
  }
  var JAVASCRIPT_CODE_BEFORE_CREATE_INPUT_HANDLER_FUNCTION = (
    // Handles any messages that're sent from the main thread.
    "var onMessage = function(data) {for (var key in data) {self[key] = data[key]}onMessage = ("
  );
  var JAVASCRIPT_CODE_AFTER_CREATE_INPUT_HANDLER_FUNCTION = ")(postMessage)}";
  function getCodeAndVars(createInputHandler, arrayOfGetDependenciesFunctions) {
    const [functionDefinitions, vars] = createFunctionsCodeAndVars(arrayOfGetDependenciesFunctions);
    const code = functionDefinitions + ";" + JAVASCRIPT_CODE_BEFORE_CREATE_INPUT_HANDLER_FUNCTION + createInputHandler.toString() + JAVASCRIPT_CODE_AFTER_CREATE_INPUT_HANDLER_FUNCTION;
    return [code, vars];
  }
  function createFunctionsCodeAndVars(arrayOfGetDependenciesFunctions) {
    let funcs = {};
    let vars = {};
    for (const getDependencies of arrayOfGetDependenciesFunctions) {
      const { functions, variables } = stringifyFunctionReferences(getDependencies);
      funcs = Object.assign(Object.assign({}, funcs), functions);
      vars = Object.assign(Object.assign({}, vars), variables);
    }
    const functionDefinitions = Object.keys(funcs).map((functionName) => {
      return functionName + "=" + funcs[functionName];
    }).join(";");
    return [functionDefinitions, vars];
  }

  // node_modules/worker-f/lib/createWorkerFunction_.js
  function createWorkerFunction_(createWorkerInEnvironment, fnOrAlias, createMethods, createInputHandler, handleError, handleOutput) {
    let started = false;
    let stopped = false;
    let getDependenciesFunctions = [];
    const dependenciesTransferList = void 0;
    let inputTransferList = () => [];
    let outputTransferList = () => [];
    let alias = void 0;
    let cacheValue = void 0;
    const getFromCache = (cacheKey) => {
      return CACHE[cacheKey];
    };
    const setInCache = (cacheKey, value) => {
      CACHE[cacheKey] = value;
    };
    const mustHaveStarted = () => {
      if (!started) {
        throw new Error("Not started");
      }
    };
    const mustNotHaveStarted = () => {
      if (started) {
        throw new Error("Was started");
      }
    };
    const mustNotHaveStopped = () => {
      if (stopped) {
        throw new Error("Was stopped");
      }
    };
    const mustNotHaveAlias = () => {
      if (alias) {
        throw new Error("Has alias");
      }
    };
    const argumentMustBeFunction = (arg) => {
      if (typeof arg !== "function") {
        throw new TypeError("Argument must be a function");
      }
    };
    let fn;
    if (typeof fnOrAlias === "string") {
      alias = fnOrAlias;
      cacheValue = getFromCache(alias);
      if (!cacheValue || !cacheValue.$) {
        throw new Error("Not found");
      }
      fn = cacheValue.$[0];
      getDependenciesFunctions = cacheValue.$[1];
      inputTransferList = cacheValue.$[2];
      outputTransferList = cacheValue.$[3];
      cacheValue.$[4];
    } else {
      fn = fnOrAlias;
    }
    argumentMustBeFunction(fn);
    let worker;
    const addDependencies_ = (getDependencies) => {
      mustNotHaveStopped();
      mustNotHaveStarted();
      argumentMustBeFunction(getDependencies);
      getDependenciesFunctions.push(getDependencies);
    };
    const start = () => {
      mustNotHaveStopped();
      mustNotHaveStarted();
      const getInitialDependencies = () => [
        fn,
        outputTransferList,
        createInputHandler
      ];
      addDependencies_(getInitialDependencies);
      started = true;
      worker.start(getDependenciesFunctions, dependenciesTransferList);
    };
    const stop = () => {
      stopped = true;
      worker.stop();
    };
    const sendToWorker = (inputArgs) => {
      worker.ingest([Date.now(), inputArgs], inputTransferList(...inputArgs));
    };
    let numberOrUndefined;
    const workerFn = Object.assign({
      inputLatency: numberOrUndefined,
      outputLatency: numberOrUndefined,
      /**
       * Adds external dependencies.
       * These dependencies must not change after the function is started.
       *
       * @param {function} getDependencies — A "closure" function that returns an array of dependencies — global variables or functions — that will be used in this worker. If some dependencies get overlooked, the worker will throw "[name] is not defined".
       */
      addDependencies(getDependencies) {
        mustNotHaveAlias();
        addDependencies_(getDependencies);
      },
      // `transferList` for the arguments of the function.
      inputTransferList: (fn2) => {
        mustNotHaveStopped();
        mustNotHaveStarted();
        mustNotHaveAlias();
        argumentMustBeFunction(fn2);
        inputTransferList = fn2;
      },
      // `transferList` for the result of the function.
      outputTransferList: (fn2) => {
        mustNotHaveStopped();
        mustNotHaveStarted();
        mustNotHaveAlias();
        argumentMustBeFunction(fn2);
        outputTransferList = fn2;
      },
      // (optional) Enables caching.
      alias(alias_) {
        mustNotHaveStopped();
        mustNotHaveStarted();
        mustNotHaveAlias();
        alias = alias_;
        setInCache(alias, {
          $: [fn, getDependenciesFunctions, inputTransferList, outputTransferList]
        });
      },
      start,
      stop
    }, createMethods(start, stop, started, stopped, sendToWorker, mustHaveStarted, mustNotHaveStarted, mustNotHaveStopped));
    const getOtherFromCache = () => {
      if (alias) {
        const properties = getFromCache(alias);
        if (properties) {
          return properties.other;
        }
      }
    };
    const setOtherInCache = (value) => {
      if (alias) {
        setInCache(alias, Object.assign(Object.assign({}, getFromCache(alias)), { other: value }));
      }
    };
    worker = createWorker(
      // Creates a worker in a specific environment such as a web browser or Node.js.
      createWorkerInEnvironment,
      // This function will be executed in the worker thread.
      // It will be stringified and injected in the worker source code.
      // It must create an input handler function.
      (sendOutput_) => {
        let inputSentTimestamp = -1;
        let inputReceivedTimestamp = -1;
        const sendOutput = (output) => {
          sendOutput_([inputSentTimestamp, inputReceivedTimestamp, Date.now(), output], outputTransferList(output));
        };
        const inputHandler = createInputHandler(fn, sendOutput);
        return ([inputSentAt, input]) => {
          inputSentTimestamp = inputSentAt;
          inputReceivedTimestamp = Date.now();
          return inputHandler(input);
        };
      },
      // This function will be executed in the main thread
      // when an error is received from the worker.
      // Currently, we are in the main thread.
      (error) => {
        if (!stopped) {
          handleError(error);
        }
      },
      // This function will be executed in the main thread
      // when output is received from the worker.
      // Currently, we are in the main thread.
      ([inputSentTimestamp, inputReceivedTimestamp, outputTimestamp, output]) => {
        if (!stopped) {
          if (inputSentTimestamp !== -1) {
            workerFn.inputLatency = inputReceivedTimestamp - inputSentTimestamp;
          }
          workerFn.outputLatency = Date.now() - outputTimestamp;
          handleOutput(output);
        }
      },
      // Caching.
      getOtherFromCache,
      setOtherInCache
    );
    return workerFn;
  }
  var CACHE = {};

  // node_modules/worker-f/lib/createWorkerFunction.js
  function createWorkerFunction(createWorkerInEnvironment, fnOrAlias) {
    let resolveCall = void 0;
    let rejectCall = void 0;
    const createMethods = (start, stop, started, stopped, sendToWorker, mustHaveStarted, mustNotHaveStarted, mustNotHaveStopped) => ({
      // Calls the function. Could be used multiple times.
      call(...args) {
        mustNotHaveStopped();
        mustHaveStarted();
        if (resolveCall || rejectCall) {
          throw new Error("Previous call not finished");
        }
        return new Promise((resolve, reject) => {
          resolveCall = resolve;
          rejectCall = reject;
          sendToWorker(args);
        });
      },
      // Calls the function once.
      callOnce(...args) {
        start();
        return this.call(...args).finally(stop);
      }
    });
    const createInputHandler = (fn, send) => {
      const isPromise2 = (anything) => {
        return anything !== null && typeof anything === "object" && typeof anything.then === "function";
      };
      return (args) => {
        const result = fn(...args);
        if (isPromise2(result)) {
          result.then(send);
        } else {
          send(result);
        }
      };
    };
    const handleError = (error) => {
      if (rejectCall) {
        rejectCall(error);
        resolveCall = void 0;
        rejectCall = void 0;
      } else {
        throw new Error("`reject` callback not found");
      }
    };
    const handleOutput = (output) => {
      if (resolveCall) {
        resolveCall(output);
        resolveCall = void 0;
        rejectCall = void 0;
      } else {
        throw new Error("`resolve` callback not found");
      }
    };
    return createWorkerFunction_(createWorkerInEnvironment, fnOrAlias, createMethods, createInputHandler, handleError, handleOutput);
  }

  // node_modules/worker-f/lib/environment/createWorkerInBrowser.js
  function createWorkerInBrowser(javascriptCode, getFromCache, setInCache, onError, onOutput) {
    let url = getFromCache();
    if (!url) {
      url = createWorkerCodeUrl(javascriptCode);
      setInCache(url);
    }
    const worker = new Worker(url);
    worker.onmessage = (event) => {
      const data = event.data;
      const errorData = data ? data[ERROR_MESSAGE_PROPERTY_NAME] : void 0;
      if (errorData) {
        const error = new Error(errorData[0]);
        error.stack = errorData[1];
        let i = 2;
        while (i < errorData.length) {
          error[errorData[i][0]] = errorData[i][1];
          i++;
        }
        onError(error);
        worker.terminate();
      } else {
        onOutput(data);
      }
    };
    return {
      // Calling `worker.terminate()` will kill the worker thread immediately.
      // Calling `worker.terminate()` multiple times is safe and will not throw any errors.
      stop: worker.terminate.bind(worker),
      ingest: worker.postMessage.bind(worker)
    };
  }
  function createWorkerCodeUrl(javascriptCode) {
    return URL.createObjectURL(new Blob([
      javascriptCode + ";" + JAVASCRIPT_CODE_ADDITIONAL
    ], { type: "text/javascript" }));
  }
  var ERROR_MESSAGE_PROPERTY_NAME = "$err$";
  var JAVASCRIPT_CODE_ADDITIONAL = (
    // Listen to incoming messages from the main thread.
    "self.onmessage = function(evt) {onMessage(evt.data)};function onErr(err_, msg) {var err = err_ instanceof Error ? err_ : new Error(msg);postMessage({" + ERROR_MESSAGE_PROPERTY_NAME + ':[err.message,err.stack].concat(Object.keys(err).map(function(k){return[k,err[k]]}))})};var postMessage = self.postMessage;addEventListener("error",function(evt) {onErr(evt.error, evt.message)});addEventListener("unhandledrejection",function(evt) {evt.preventDefault();onErr(evt.reason, String(evt.reason))})'
  );

  // node_modules/worker-f/lib/export/browser/createWorkerFunctionInBrowser.js
  function createWorkerFunctionInBrowser(fnOrAlias) {
    return createWorkerFunction(createWorkerInBrowser, fnOrAlias);
  }

  // node_modules/read-excel-file/modules/saxen/parser.js
  function _typeof(o) {
    "@babel/helpers - typeof";
    return _typeof = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof(o);
  }
  function Parser_(options) {
    var fromCharCode = String.fromCharCode;
    var hasOwnProperty = Object.prototype.hasOwnProperty;
    var ENTITY_PATTERN = /&#(\d+);|&#x([0-9a-f]+);|&(\w+);/ig;
    var ENTITY_MAPPING = {
      "amp": "&",
      "apos": "'",
      "gt": ">",
      "lt": "<",
      "quot": '"'
    };
    Object.keys(ENTITY_MAPPING).forEach(function(k) {
      ENTITY_MAPPING[k.toUpperCase()] = ENTITY_MAPPING[k];
    });
    function replaceEntities(_, d, x, z) {
      if (z) {
        if (hasOwnProperty.call(ENTITY_MAPPING, z)) {
          return ENTITY_MAPPING[z];
        } else {
          return "&" + z + ";";
        }
      }
      if (d) {
        return fromCharCode(d);
      }
      return fromCharCode(parseInt(x, 16));
    }
    function decodeEntities(s) {
      if (s.length > 3 && s.indexOf("&") !== -1) {
        return s.replace(ENTITY_PATTERN, replaceEntities);
      }
      return s;
    }
    var NON_WHITESPACE_OUTSIDE_ROOT_NODE = "non-whitespace outside of root node";
    function error(msg) {
      return new Error(msg);
    }
    function missingNamespaceForPrefix(prefix) {
      return "missing namespace for prefix <" + prefix + ">";
    }
    function getter(getFn) {
      return {
        "get": getFn,
        "enumerable": true
      };
    }
    function cloneNsMatrix(nsMatrix) {
      var clone = {}, key2;
      for (key2 in nsMatrix) {
        clone[key2] = nsMatrix[key2];
      }
      return clone;
    }
    var NAME_CACHE = Symbol("nameCache");
    function uriPrefix(prefix) {
      return prefix + "$uri";
    }
    function buildNsMatrix(nsUriToPrefix) {
      var nsMatrix = {}, uri, prefix;
      for (uri in nsUriToPrefix) {
        prefix = nsUriToPrefix[uri];
        nsMatrix[prefix] = prefix;
        nsMatrix[uriPrefix(prefix)] = uri;
      }
      return nsMatrix;
    }
    function noopGetContext() {
      return {
        line: 0,
        column: 0
      };
    }
    function throwFunc(err2) {
      throw err2;
    }
    function Parser(options2) {
      if (!this) {
        return new Parser(options2);
      }
      var proxy = options2 && options2["proxy"];
      var onText, onOpenTag, onCloseTag, onCDATA, onError = throwFunc, onWarning, onComment, onQuestion, onAttention;
      var getContext = noopGetContext;
      var streaming = false;
      var rootTagFound = false;
      var leftoverXml = "";
      var maybeNS = false;
      var isNamespace = false;
      var returnError = null;
      var parseStop = false;
      var nsMatrixStack, nsMatrix, nodeStack;
      var nsUriToPrefix;
      function handleError(err2) {
        if (!(err2 instanceof Error)) {
          err2 = error(err2);
        }
        returnError = err2;
        onError(err2, getContext);
      }
      function handleWarning(err2) {
        if (!onWarning) {
          return;
        }
        if (!(err2 instanceof Error)) {
          err2 = error(err2);
        }
        onWarning(err2, getContext);
      }
      this["on"] = function(name, cb) {
        if (typeof cb !== "function") {
          throw error("required args <name, cb>");
        }
        switch (name) {
          case "openTag":
            onOpenTag = cb;
            break;
          case "text":
            onText = cb;
            break;
          case "closeTag":
            onCloseTag = cb;
            break;
          case "error":
            onError = cb;
            break;
          case "warn":
            onWarning = cb;
            break;
          case "cdata":
            onCDATA = cb;
            break;
          case "attention":
            onAttention = cb;
            break;
          // <!XXXXX zzzz="eeee">
          case "question":
            onQuestion = cb;
            break;
          // <? ....  ?>
          case "comment":
            onComment = cb;
            break;
          default:
            throw error("unsupported event: " + name);
        }
        return this;
      };
      this["ns"] = function(nsMap) {
        if (typeof nsMap === "undefined") {
          nsMap = {};
        }
        if (_typeof(nsMap) !== "object") {
          throw error("required args <nsMap={}>");
        }
        var _nsUriToPrefix = {}, k;
        for (k in nsMap) {
          _nsUriToPrefix[k] = nsMap[k];
        }
        isNamespace = true;
        nsUriToPrefix = _nsUriToPrefix;
        return this;
      };
      function resetState() {
        nsMatrixStack = isNamespace ? [] : null;
        nsMatrix = isNamespace ? buildNsMatrix(nsUriToPrefix) : null;
        nodeStack = [];
        getContext = noopGetContext;
        parseStop = false;
        returnError = null;
        rootTagFound = false;
        leftoverXml = "";
      }
      this["parse"] = function(xml) {
        if (typeof xml !== "string") {
          throw error("required args <xml=string>");
        }
        if (streaming) {
          throw error("parse during stream; call end() first");
        }
        resetState();
        parse(xml);
        getContext = noopGetContext;
        parseStop = false;
        return returnError;
      };
      this["write"] = function(xml) {
        if (typeof xml !== "string") {
          throw error("required args <xml=string>");
        }
        if (!streaming) {
          resetState();
          streaming = true;
        }
        if (!returnError) {
          leftoverXml = parse(leftoverXml + xml, true) || "";
        }
        return this;
      };
      this["end"] = function() {
        if (!streaming) {
          resetState();
        }
        streaming = false;
        if (!returnError) {
          parse(leftoverXml);
        }
        leftoverXml = "";
        getContext = noopGetContext;
        parseStop = false;
        return returnError;
      };
      this["stop"] = function() {
        parseStop = true;
      };
      function parse(xml) {
        var streaming2 = arguments.length > 1 && arguments[1] !== void 0 ? arguments[1] : false;
        var elNameCache = null, elNameCacheMatrix = null;
        var _nsMatrix, anonymousNsCount = 0, tagStart = false, tagEnd = false, i = 0, j = 0, x, y, q, w, v, xmlns, elementName, _elementName, elementProxy;
        var attrsString = "", attrsStart = 0, cachedAttrs;
        function normalizeAttrName(name, defaultAlias) {
          var w2 = name.indexOf(":");
          if (w2 === -1) {
            return name;
          }
          var nsName = nsMatrix[name.substring(0, w2)];
          if (!nsName) {
            handleWarning(missingNamespaceForPrefix(name.substring(0, w2)));
            return null;
          }
          return defaultAlias === nsName ? name.substr(w2 + 1) : nsName + name.substr(w2);
        }
        function getAttrs() {
          if (cachedAttrs !== null) {
            return cachedAttrs;
          }
          var nsUri, nsUriPrefix, defaultAlias = isNamespace && nsMatrix["xmlns"], attrList = isNamespace && maybeNS ? [] : null, i2 = attrsStart, s = attrsString, l = s.length, hasNewMatrix, newalias, value, alias, name, attrs = {}, seenAttrs = /* @__PURE__ */ new Set(), skipAttr, w2, j2;
          parseAttr: for (; i2 < l; i2++) {
            skipAttr = false;
            w2 = s.charCodeAt(i2);
            if (w2 === 32 || w2 < 14 && w2 > 8) {
              continue;
            }
            if (w2 < 65 || w2 > 122 || w2 > 90 && w2 < 97) {
              if (w2 !== 95 && w2 !== 58) {
                handleWarning("illegal first char attribute name");
                skipAttr = true;
              }
            }
            for (j2 = i2 + 1; j2 < l; j2++) {
              w2 = s.charCodeAt(j2);
              if (w2 > 96 && w2 < 123 || w2 > 64 && w2 < 91 || w2 > 47 && w2 < 59 || w2 === 46 || // '.'
              w2 === 45 || // '-'
              w2 === 95) {
                continue;
              }
              if (w2 === 32 || w2 < 14 && w2 > 8) {
                handleWarning("missing attribute value");
                i2 = j2;
                continue parseAttr;
              }
              if (w2 === 61) {
                break;
              }
              handleWarning("illegal attribute name char");
              skipAttr = true;
            }
            name = s.substring(i2, j2);
            if (name === "xmlns:xmlns") {
              handleWarning("illegal declaration of xmlns");
              skipAttr = true;
            }
            w2 = s.charCodeAt(j2 + 1);
            if (w2 === 34) {
              j2 = s.indexOf('"', i2 = j2 + 2);
              if (j2 === -1) {
                j2 = s.indexOf("'", i2);
                if (j2 !== -1) {
                  handleWarning("attribute value quote missmatch");
                  skipAttr = true;
                }
              }
            } else if (w2 === 39) {
              j2 = s.indexOf("'", i2 = j2 + 2);
              if (j2 === -1) {
                j2 = s.indexOf('"', i2);
                if (j2 !== -1) {
                  handleWarning("attribute value quote missmatch");
                  skipAttr = true;
                }
              }
            } else {
              handleWarning("missing attribute value quotes");
              skipAttr = true;
              for (j2 = j2 + 1; j2 < l; j2++) {
                w2 = s.charCodeAt(j2 + 1);
                if (w2 === 32 || w2 < 14 && w2 > 8) {
                  break;
                }
              }
            }
            if (j2 === -1) {
              handleWarning("missing closing quotes");
              j2 = l;
              skipAttr = true;
            }
            if (!skipAttr) {
              value = s.substring(i2, j2);
            }
            i2 = j2;
            for (; j2 + 1 < l; j2++) {
              w2 = s.charCodeAt(j2 + 1);
              if (w2 === 32 || w2 < 14 && w2 > 8) {
                break;
              }
              if (i2 === j2) {
                handleWarning("illegal character after attribute end");
                skipAttr = true;
              }
            }
            i2 = j2 + 1;
            if (skipAttr) {
              continue parseAttr;
            }
            if (seenAttrs.has(name)) {
              handleWarning("attribute <" + name + "> already defined");
              continue;
            }
            seenAttrs.add(name);
            if (!isNamespace) {
              attrs[name] = value;
              continue;
            }
            if (maybeNS) {
              newalias = name === "xmlns" ? "xmlns" : name.charCodeAt(0) === 120 && name.substr(0, 6) === "xmlns:" ? name.substr(6) : null;
              if (newalias !== null) {
                nsUri = decodeEntities(value);
                nsUriPrefix = uriPrefix(newalias);
                alias = nsUriToPrefix[nsUri];
                if (!alias) {
                  if (newalias === "xmlns" || nsUriPrefix in nsMatrix && nsMatrix[nsUriPrefix] !== nsUri) {
                    do {
                      alias = "ns" + anonymousNsCount++;
                    } while (typeof nsMatrix[alias] !== "undefined");
                  } else {
                    alias = newalias;
                  }
                  nsUriToPrefix[nsUri] = alias;
                }
                if (nsMatrix[newalias] !== alias) {
                  if (!hasNewMatrix) {
                    nsMatrix = cloneNsMatrix(nsMatrix);
                    hasNewMatrix = true;
                  }
                  nsMatrix[newalias] = alias;
                  if (newalias === "xmlns") {
                    nsMatrix[uriPrefix(alias)] = nsUri;
                    defaultAlias = alias;
                  }
                  nsMatrix[nsUriPrefix] = nsUri;
                }
                attrs[name] = value;
                continue;
              }
              attrList.push(name, value);
              continue;
            }
            name = normalizeAttrName(name, defaultAlias);
            if (name === null) {
              continue;
            }
            attrs[name] = value;
          }
          if (maybeNS) {
            for (i2 = 0, l = attrList.length; i2 < l; i2++) {
              name = normalizeAttrName(attrList[i2++], defaultAlias);
              value = attrList[i2];
              if (name === null) {
                continue;
              }
              attrs[name] = value;
            }
          }
          return cachedAttrs = attrs;
        }
        function getParseContext() {
          var splitsRe = /(\r\n|\r|\n)/g;
          var line = 0;
          var column2 = 0;
          var startOfLine = 0;
          var endOfLine = j;
          var match;
          var data;
          while (i >= startOfLine) {
            match = splitsRe.exec(xml);
            if (!match) {
              break;
            }
            endOfLine = match[0].length + match.index;
            if (endOfLine > i) {
              break;
            }
            line += 1;
            startOfLine = endOfLine;
          }
          if (i == -1) {
            column2 = endOfLine;
            data = xml.substring(j);
          } else if (j === 0) {
            data = xml.substring(j, i);
          } else {
            column2 = i - startOfLine;
            data = j == -1 ? xml.substring(i) : xml.substring(i, j + 1);
          }
          return {
            "data": data,
            "line": line,
            "column": column2
          };
        }
        getContext = getParseContext;
        if (proxy) {
          elementProxy = Object.create({}, {
            "name": getter(function() {
              return elementName;
            }),
            "originalName": getter(function() {
              return _elementName;
            }),
            "attrs": getter(getAttrs),
            "ns": getter(function() {
              return nsMatrix;
            })
          });
        }
        while (j !== -1) {
          if (xml.charCodeAt(j) === 60) {
            i = j;
          } else {
            i = xml.indexOf("<", j);
          }
          if (i === -1) {
            if (streaming2) {
              return xml.substring(j);
            }
            if (nodeStack.length) {
              return handleError("unexpected end of file");
            }
            if (!rootTagFound) {
              return handleError("missing start tag");
            }
            if (j < xml.length) {
              if (xml.substring(j).trim()) {
                handleWarning(NON_WHITESPACE_OUTSIDE_ROOT_NODE);
              }
            }
            return;
          }
          if (!rootTagFound) {
            rootTagFound = true;
          }
          if (j !== i) {
            if (nodeStack.length) {
              if (onText) {
                onText(xml.substring(j, i), decodeEntities, getContext);
                if (parseStop) {
                  return;
                }
              }
            } else {
              if (xml.substring(j, i).trim()) {
                handleWarning(NON_WHITESPACE_OUTSIDE_ROOT_NODE);
                if (parseStop) {
                  return;
                }
              }
            }
          }
          w = xml.charCodeAt(i + 1);
          if (w === 33) {
            q = xml.charCodeAt(i + 2);
            if (q === 91 && xml.substr(i + 3, 6) === "CDATA[") {
              j = xml.indexOf("]]>", i);
              if (j === -1) {
                if (streaming2) {
                  return xml.substring(i);
                }
                return handleError("unclosed cdata");
              }
              if (onCDATA) {
                onCDATA(xml.substring(i + 9, j), getContext);
                if (parseStop) {
                  return;
                }
              }
              j += 3;
              continue;
            }
            if (q === 45 && xml.charCodeAt(i + 3) === 45) {
              j = xml.indexOf("-->", i);
              if (j === -1) {
                if (streaming2) {
                  return xml.substring(i);
                }
                return handleError("unclosed comment");
              }
              if (onComment) {
                onComment(xml.substring(i + 4, j), decodeEntities, getContext);
                if (parseStop) {
                  return;
                }
              }
              j += 3;
              continue;
            }
          }
          if (w === 63) {
            j = xml.indexOf("?>", i);
            if (j === -1) {
              if (streaming2) {
                return xml.substring(i);
              }
              return handleError("unclosed question");
            }
            if (onQuestion) {
              onQuestion(xml.substring(i, j + 2), getContext);
              if (parseStop) {
                return;
              }
            }
            j += 2;
            continue;
          }
          for (x = i + 1; ; x++) {
            v = xml.charCodeAt(x);
            if (isNaN(v)) {
              if (streaming2) {
                return xml.substring(i);
              }
              j = -1;
              return handleError("unclosed tag");
            }
            if (v === 34) {
              q = xml.indexOf('"', x + 1);
              x = q !== -1 ? q : x;
            } else if (v === 39) {
              q = xml.indexOf("'", x + 1);
              x = q !== -1 ? q : x;
            } else if (v === 62) {
              j = x;
              break;
            }
          }
          if (w === 33) {
            if (onAttention) {
              onAttention(xml.substring(i, j + 1), decodeEntities, getContext);
              if (parseStop) {
                return;
              }
            }
            j += 1;
            continue;
          }
          cachedAttrs = {};
          if (w === 47) {
            tagStart = false;
            tagEnd = true;
            if (!nodeStack.length) {
              return handleError("missing open tag");
            }
            x = elementName = nodeStack.pop();
            q = i + 2 + x.length;
            if (xml.substring(i + 2, q) !== x) {
              return handleError("closing tag mismatch");
            }
            for (; q < j; q++) {
              w = xml.charCodeAt(q);
              if (w === 32 || w > 8 && w < 14) {
                continue;
              }
              return handleError("close tag");
            }
          } else {
            if (xml.charCodeAt(j - 1) === 47) {
              x = elementName = xml.substring(i + 1, j - 1);
              tagStart = true;
              tagEnd = true;
            } else {
              x = elementName = xml.substring(i + 1, j);
              tagStart = true;
              tagEnd = false;
            }
            if (!(w > 96 && w < 123 || w > 64 && w < 91 || w === 95 || w === 58)) {
              return handleError("illegal first char nodeName");
            }
            for (q = 1, y = x.length; q < y; q++) {
              w = x.charCodeAt(q);
              if (w > 96 && w < 123 || w > 64 && w < 91 || w > 47 && w < 59 || w === 45 || w === 95 || w == 46) {
                continue;
              }
              if (w === 32 || w < 14 && w > 8) {
                elementName = x.substring(0, q);
                cachedAttrs = null;
                break;
              }
              return handleError("invalid nodeName");
            }
            if (!tagEnd) {
              nodeStack.push(elementName);
            }
          }
          if (isNamespace) {
            _nsMatrix = nsMatrix;
            if (tagStart) {
              if (!tagEnd) {
                nsMatrixStack.push(_nsMatrix);
              }
              if (cachedAttrs === null) {
                if (maybeNS = x.indexOf("xmlns", q) !== -1) {
                  attrsStart = q;
                  attrsString = x;
                  getAttrs();
                  maybeNS = false;
                }
              }
            }
            _elementName = elementName;
            if (elNameCacheMatrix !== nsMatrix) {
              elNameCache = nsMatrix[NAME_CACHE];
              if (elNameCache === void 0) {
                elNameCache = nsMatrix[NAME_CACHE] = {};
              }
              elNameCacheMatrix = nsMatrix;
            }
            var _cachedName = elNameCache[elementName];
            if (_cachedName !== void 0) {
              elementName = _cachedName;
            } else {
              w = elementName.indexOf(":");
              if (w !== -1) {
                xmlns = nsMatrix[elementName.substring(0, w)];
                if (!xmlns) {
                  return handleError("missing namespace on <" + _elementName + ">");
                }
                elementName = elementName.substr(w + 1);
              } else {
                xmlns = nsMatrix["xmlns"];
              }
              if (xmlns) {
                elementName = xmlns + ":" + elementName;
              }
              elNameCache[_elementName] = elementName;
            }
          }
          if (tagStart) {
            attrsStart = q;
            attrsString = x;
            if (onOpenTag) {
              if (proxy) {
                onOpenTag(elementProxy, decodeEntities, tagEnd, getContext);
              } else {
                onOpenTag(elementName, getAttrs, decodeEntities, tagEnd, getContext);
              }
              if (parseStop) {
                return;
              }
            }
          }
          if (tagEnd) {
            if (onCloseTag) {
              onCloseTag(proxy ? elementProxy : elementName, decodeEntities, tagStart, getContext);
              if (parseStop) {
                return;
              }
            }
            if (isNamespace) {
              if (!tagStart) {
                nsMatrix = nsMatrixStack.pop();
              } else {
                nsMatrix = _nsMatrix;
              }
            }
          }
          j += 1;
        }
      }
    }
    return new Parser(options);
  }

  // node_modules/read-excel-file/modules/xml/parseXmlStream.saxen.js
  function parseXmlStream(state, onOpenTag, onCloseTag, onText) {
    var errored = false;
    var mustNotHaveErrored = function mustNotHaveErrored2() {
      if (errored) {
        throw new Error("Errored");
      }
    };
    var resolvePromise;
    var parser = new Parser_();
    var write = function write2(xml) {
      mustNotHaveErrored();
      parser.write(xml);
    };
    var end = function end2() {
      mustNotHaveErrored();
      parser.end();
      resolvePromise();
    };
    var promise = new Promise(function(resolve, reject) {
      resolvePromise = resolve;
      var onerror = function onerror2(error) {
        errored = true;
        throw error;
      };
      var ontext = function ontext2(text2, decodeEntities) {
        if (onText) {
          onText(decodeEntities(text2), state);
        }
      };
      var onopentag = function onopentag2(elementName, getAttributes, decodeEntities, selfClosing, getContext) {
        if (onOpenTag) {
          var attributes = getAttributes();
          for (var name in attributes) {
            attributes[trimXmlnsPrefix(name, true)] = decodeEntities(attributes[name]);
          }
          onOpenTag(trimXmlnsPrefix(elementName), attributes, state);
        }
      };
      var onclosetag = function onclosetag2(elementName) {
        if (onCloseTag) {
          onCloseTag(trimXmlnsPrefix(elementName), state);
        }
      };
      parser.on("error", onerror);
      parser.on("text", ontext);
      parser.on("openTag", onopentag);
      parser.on("closeTag", onclosetag);
    });
    return {
      promise,
      write,
      end
    };
  }
  function trimXmlnsPrefix(string, isAttributeName) {
    var i = 0;
    while (i < string.length) {
      if (string[i] === ":") {
        if (isAttributeName && i === 5 && string.slice(0, 5) === "xmlns") {
        } else {
          return string.slice(i + 1);
        }
      }
      i++;
    }
    return string;
  }

  // node_modules/read-excel-file/modules/xlsx/InvalidSpreadsheetError.js
  function _typeof2(o) {
    "@babel/helpers - typeof";
    return _typeof2 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof2(o);
  }
  function _defineProperties(target, props) {
    for (var i = 0; i < props.length; i++) {
      var descriptor = props[i];
      descriptor.enumerable = descriptor.enumerable || false;
      descriptor.configurable = true;
      if ("value" in descriptor) descriptor.writable = true;
      Object.defineProperty(target, _toPropertyKey(descriptor.key), descriptor);
    }
  }
  function _createClass(Constructor, protoProps, staticProps) {
    if (protoProps) _defineProperties(Constructor.prototype, protoProps);
    if (staticProps) _defineProperties(Constructor, staticProps);
    Object.defineProperty(Constructor, "prototype", { writable: false });
    return Constructor;
  }
  function _toPropertyKey(arg) {
    var key2 = _toPrimitive(arg, "string");
    return _typeof2(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive(input, hint) {
    if (_typeof2(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof2(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _classCallCheck(instance, Constructor) {
    if (!(instance instanceof Constructor)) {
      throw new TypeError("Cannot call a class as a function");
    }
  }
  function _inherits(subClass, superClass) {
    if (typeof superClass !== "function" && superClass !== null) {
      throw new TypeError("Super expression must either be null or a function");
    }
    subClass.prototype = Object.create(superClass && superClass.prototype, { constructor: { value: subClass, writable: true, configurable: true } });
    Object.defineProperty(subClass, "prototype", { writable: false });
    if (superClass) _setPrototypeOf(subClass, superClass);
  }
  function _createSuper(Derived) {
    var hasNativeReflectConstruct = _isNativeReflectConstruct();
    return function _createSuperInternal() {
      var Super = _getPrototypeOf(Derived), result;
      if (hasNativeReflectConstruct) {
        var NewTarget = _getPrototypeOf(this).constructor;
        result = Reflect.construct(Super, arguments, NewTarget);
      } else {
        result = Super.apply(this, arguments);
      }
      return _possibleConstructorReturn(this, result);
    };
  }
  function _possibleConstructorReturn(self, call) {
    if (call && (_typeof2(call) === "object" || typeof call === "function")) {
      return call;
    } else if (call !== void 0) {
      throw new TypeError("Derived constructors may only return object or undefined");
    }
    return _assertThisInitialized(self);
  }
  function _assertThisInitialized(self) {
    if (self === void 0) {
      throw new ReferenceError("this hasn't been initialised - super() hasn't been called");
    }
    return self;
  }
  function _wrapNativeSuper(Class) {
    var _cache = typeof Map === "function" ? /* @__PURE__ */ new Map() : void 0;
    _wrapNativeSuper = function _wrapNativeSuper6(Class2) {
      if (Class2 === null || !_isNativeFunction(Class2)) return Class2;
      if (typeof Class2 !== "function") {
        throw new TypeError("Super expression must either be null or a function");
      }
      if (typeof _cache !== "undefined") {
        if (_cache.has(Class2)) return _cache.get(Class2);
        _cache.set(Class2, Wrapper);
      }
      function Wrapper() {
        return _construct(Class2, arguments, _getPrototypeOf(this).constructor);
      }
      Wrapper.prototype = Object.create(Class2.prototype, { constructor: { value: Wrapper, enumerable: false, writable: true, configurable: true } });
      return _setPrototypeOf(Wrapper, Class2);
    };
    return _wrapNativeSuper(Class);
  }
  function _construct(Parent, args, Class) {
    if (_isNativeReflectConstruct()) {
      _construct = Reflect.construct.bind();
    } else {
      _construct = function _construct6(Parent2, args2, Class2) {
        var a = [null];
        a.push.apply(a, args2);
        var Constructor = Function.bind.apply(Parent2, a);
        var instance = new Constructor();
        if (Class2) _setPrototypeOf(instance, Class2.prototype);
        return instance;
      };
    }
    return _construct.apply(null, arguments);
  }
  function _isNativeReflectConstruct() {
    if (typeof Reflect === "undefined" || !Reflect.construct) return false;
    if (Reflect.construct.sham) return false;
    if (typeof Proxy === "function") return true;
    try {
      Boolean.prototype.valueOf.call(Reflect.construct(Boolean, [], function() {
      }));
      return true;
    } catch (e) {
      return false;
    }
  }
  function _isNativeFunction(fn) {
    return Function.toString.call(fn).indexOf("[native code]") !== -1;
  }
  function _setPrototypeOf(o, p) {
    _setPrototypeOf = Object.setPrototypeOf ? Object.setPrototypeOf.bind() : function _setPrototypeOf6(o2, p2) {
      o2.__proto__ = p2;
      return o2;
    };
    return _setPrototypeOf(o, p);
  }
  function _getPrototypeOf(o) {
    _getPrototypeOf = Object.setPrototypeOf ? Object.getPrototypeOf.bind() : function _getPrototypeOf6(o2) {
      return o2.__proto__ || Object.getPrototypeOf(o2);
    };
    return _getPrototypeOf(o);
  }
  var InvalidSpreadsheetError = /* @__PURE__ */ function(_Error) {
    _inherits(InvalidSpreadsheetError2, _Error);
    var _super = _createSuper(InvalidSpreadsheetError2);
    function InvalidSpreadsheetError2(message) {
      var _this;
      _classCallCheck(this, InvalidSpreadsheetError2);
      _this = _super.call(this, message);
      _this.name = "InvalidSpreadsheetError";
      return _this;
    }
    return _createClass(InvalidSpreadsheetError2);
  }(/* @__PURE__ */ _wrapNativeSuper(Error));

  // node_modules/read-excel-file/modules/xml/parseXml.js
  function parseXml(xml, state, onOpenTag, onCloseTag, onText, onProgress) {
    var parser = parseXmlStream(state, onOpenTag, onCloseTag, onText);
    if (onProgress) {
      parseXmlInChunks(parser, xml, onProgress);
    } else {
      parser.write(xml);
      parser.end();
    }
    return parser.promise.then(function(result) {
      return result;
    }, function(error) {
      var spreadsheetError = new InvalidSpreadsheetError(error.message);
      spreadsheetError.stack = error.stack;
      spreadsheetError.cause = error;
      throw spreadsheetError;
    });
    function parseXmlInChunks(parser2, xml2, onProgress2, nonBlocking) {
      var MAX_CHUNK_PROCESSING_TIME = 7;
      var INITIAL_CHUNK_SIZE = 64 * 1024;
      var chunksCount = 0;
      var chunkSize = INITIAL_CHUNK_SIZE;
      var parseNextChunk = function parseNextChunk2() {
        chunksCount++;
        var startedAt = Date.now();
        if (xml2.length > chunkSize) {
          parser2.write(xml2.slice(0, chunkSize));
          if (onProgress2) {
            onProgress2(false);
          }
          xml2 = xml2.slice(chunkSize);
          var chunkProcessingTime = Date.now() - startedAt;
          if (chunkProcessingTime < MAX_CHUNK_PROCESSING_TIME * 0.5) {
            chunkSize *= 2;
          } else if (chunkProcessingTime > MAX_CHUNK_PROCESSING_TIME) {
            chunkSize /= 2;
          }
          return true;
        } else {
          parser2.write(xml2);
          parser2.end();
          if (onProgress2) {
            onProgress2(true);
          }
          return false;
        }
      };
      var loop = function loop2() {
        if (parseNextChunk()) {
          if (nonBlocking) {
            if (typeof setImmediate !== "undefined") {
              setImmediate(loop2);
            } else {
              setTimeout(loop2, 0);
            }
          } else {
            loop2();
          }
        } else {
        }
      };
      loop();
    }
  }

  // node_modules/read-excel-file/modules/zip/UnzipError.js
  function _typeof3(o) {
    "@babel/helpers - typeof";
    return _typeof3 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof3(o);
  }
  function _defineProperties2(target, props) {
    for (var i = 0; i < props.length; i++) {
      var descriptor = props[i];
      descriptor.enumerable = descriptor.enumerable || false;
      descriptor.configurable = true;
      if ("value" in descriptor) descriptor.writable = true;
      Object.defineProperty(target, _toPropertyKey2(descriptor.key), descriptor);
    }
  }
  function _createClass2(Constructor, protoProps, staticProps) {
    if (protoProps) _defineProperties2(Constructor.prototype, protoProps);
    if (staticProps) _defineProperties2(Constructor, staticProps);
    Object.defineProperty(Constructor, "prototype", { writable: false });
    return Constructor;
  }
  function _toPropertyKey2(arg) {
    var key2 = _toPrimitive2(arg, "string");
    return _typeof3(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive2(input, hint) {
    if (_typeof3(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof3(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _classCallCheck2(instance, Constructor) {
    if (!(instance instanceof Constructor)) {
      throw new TypeError("Cannot call a class as a function");
    }
  }
  function _inherits2(subClass, superClass) {
    if (typeof superClass !== "function" && superClass !== null) {
      throw new TypeError("Super expression must either be null or a function");
    }
    subClass.prototype = Object.create(superClass && superClass.prototype, { constructor: { value: subClass, writable: true, configurable: true } });
    Object.defineProperty(subClass, "prototype", { writable: false });
    if (superClass) _setPrototypeOf2(subClass, superClass);
  }
  function _createSuper2(Derived) {
    var hasNativeReflectConstruct = _isNativeReflectConstruct2();
    return function _createSuperInternal() {
      var Super = _getPrototypeOf2(Derived), result;
      if (hasNativeReflectConstruct) {
        var NewTarget = _getPrototypeOf2(this).constructor;
        result = Reflect.construct(Super, arguments, NewTarget);
      } else {
        result = Super.apply(this, arguments);
      }
      return _possibleConstructorReturn2(this, result);
    };
  }
  function _possibleConstructorReturn2(self, call) {
    if (call && (_typeof3(call) === "object" || typeof call === "function")) {
      return call;
    } else if (call !== void 0) {
      throw new TypeError("Derived constructors may only return object or undefined");
    }
    return _assertThisInitialized2(self);
  }
  function _assertThisInitialized2(self) {
    if (self === void 0) {
      throw new ReferenceError("this hasn't been initialised - super() hasn't been called");
    }
    return self;
  }
  function _wrapNativeSuper2(Class) {
    var _cache = typeof Map === "function" ? /* @__PURE__ */ new Map() : void 0;
    _wrapNativeSuper2 = function _wrapNativeSuper6(Class2) {
      if (Class2 === null || !_isNativeFunction2(Class2)) return Class2;
      if (typeof Class2 !== "function") {
        throw new TypeError("Super expression must either be null or a function");
      }
      if (typeof _cache !== "undefined") {
        if (_cache.has(Class2)) return _cache.get(Class2);
        _cache.set(Class2, Wrapper);
      }
      function Wrapper() {
        return _construct2(Class2, arguments, _getPrototypeOf2(this).constructor);
      }
      Wrapper.prototype = Object.create(Class2.prototype, { constructor: { value: Wrapper, enumerable: false, writable: true, configurable: true } });
      return _setPrototypeOf2(Wrapper, Class2);
    };
    return _wrapNativeSuper2(Class);
  }
  function _construct2(Parent, args, Class) {
    if (_isNativeReflectConstruct2()) {
      _construct2 = Reflect.construct.bind();
    } else {
      _construct2 = function _construct6(Parent2, args2, Class2) {
        var a = [null];
        a.push.apply(a, args2);
        var Constructor = Function.bind.apply(Parent2, a);
        var instance = new Constructor();
        if (Class2) _setPrototypeOf2(instance, Class2.prototype);
        return instance;
      };
    }
    return _construct2.apply(null, arguments);
  }
  function _isNativeReflectConstruct2() {
    if (typeof Reflect === "undefined" || !Reflect.construct) return false;
    if (Reflect.construct.sham) return false;
    if (typeof Proxy === "function") return true;
    try {
      Boolean.prototype.valueOf.call(Reflect.construct(Boolean, [], function() {
      }));
      return true;
    } catch (e) {
      return false;
    }
  }
  function _isNativeFunction2(fn) {
    return Function.toString.call(fn).indexOf("[native code]") !== -1;
  }
  function _setPrototypeOf2(o, p) {
    _setPrototypeOf2 = Object.setPrototypeOf ? Object.setPrototypeOf.bind() : function _setPrototypeOf6(o2, p2) {
      o2.__proto__ = p2;
      return o2;
    };
    return _setPrototypeOf2(o, p);
  }
  function _getPrototypeOf2(o) {
    _getPrototypeOf2 = Object.setPrototypeOf ? Object.getPrototypeOf.bind() : function _getPrototypeOf6(o2) {
      return o2.__proto__ || Object.getPrototypeOf(o2);
    };
    return _getPrototypeOf2(o);
  }
  var UnzipError = /* @__PURE__ */ function(_Error) {
    _inherits2(UnzipError2, _Error);
    var _super = _createSuper2(UnzipError2);
    function UnzipError2() {
      _classCallCheck2(this, UnzipError2);
      return _super.apply(this, arguments);
    }
    return _createClass2(UnzipError2);
  }(/* @__PURE__ */ _wrapNativeSuper2(Error));
  function createUnzipError(error) {
    var unzipError = new UnzipError(error.message);
    if (error.stack) {
      unzipError.stack = error.stack;
    }
    if (Error.captureStackTrace) {
      Error.captureStackTrace(unzipError, createUnzipError);
    }
    unzipError.cause = error;
    return unzipError;
  }

  // node_modules/read-excel-file/modules/zip/unzipFromArrayBuffer.js
  function unzipFromArrayBuffer(input, options) {
    return unzipFromArrayBufferUsingFunction(input, options, unzipAsync, true);
  }
  function unzipFromArrayBufferUsingFunction(input) {
    var _ref = arguments.length > 1 && arguments[1] !== void 0 ? arguments[1] : {}, _filter = _ref.filter;
    var unzip2 = arguments.length > 2 ? arguments[2] : void 0;
    var isAsync = arguments.length > 3 ? arguments[3] : void 0;
    return unzip2(new Uint8Array(input), {
      // Ignore certain types of files.
      filter: function filter(file) {
        if (_filter) {
          return _filter({
            path: file.name
          });
        }
        return true;
      }
    }).then(function(result) {
      return result;
    }, function(error) {
      if (isFlateError(error)) {
        throw createUnzipError(error);
      } else {
        throw error;
      }
    });
  }
  function unzipAsync(archive) {
    return new Promise(function(resolve, reject) {
      unzip(archive, function(error, files) {
        if (error) {
          reject(error);
        } else {
          resolve(files);
        }
      });
    });
  }
  function isFlateError(error) {
    return typeof error.code === "number";
  }

  // node_modules/read-excel-file/modules/xlsx/file/InvalidInputError.js
  function _typeof4(o) {
    "@babel/helpers - typeof";
    return _typeof4 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof4(o);
  }
  function _defineProperties3(target, props) {
    for (var i = 0; i < props.length; i++) {
      var descriptor = props[i];
      descriptor.enumerable = descriptor.enumerable || false;
      descriptor.configurable = true;
      if ("value" in descriptor) descriptor.writable = true;
      Object.defineProperty(target, _toPropertyKey3(descriptor.key), descriptor);
    }
  }
  function _createClass3(Constructor, protoProps, staticProps) {
    if (protoProps) _defineProperties3(Constructor.prototype, protoProps);
    if (staticProps) _defineProperties3(Constructor, staticProps);
    Object.defineProperty(Constructor, "prototype", { writable: false });
    return Constructor;
  }
  function _toPropertyKey3(arg) {
    var key2 = _toPrimitive3(arg, "string");
    return _typeof4(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive3(input, hint) {
    if (_typeof4(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof4(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _classCallCheck3(instance, Constructor) {
    if (!(instance instanceof Constructor)) {
      throw new TypeError("Cannot call a class as a function");
    }
  }
  function _inherits3(subClass, superClass) {
    if (typeof superClass !== "function" && superClass !== null) {
      throw new TypeError("Super expression must either be null or a function");
    }
    subClass.prototype = Object.create(superClass && superClass.prototype, { constructor: { value: subClass, writable: true, configurable: true } });
    Object.defineProperty(subClass, "prototype", { writable: false });
    if (superClass) _setPrototypeOf3(subClass, superClass);
  }
  function _createSuper3(Derived) {
    var hasNativeReflectConstruct = _isNativeReflectConstruct3();
    return function _createSuperInternal() {
      var Super = _getPrototypeOf3(Derived), result;
      if (hasNativeReflectConstruct) {
        var NewTarget = _getPrototypeOf3(this).constructor;
        result = Reflect.construct(Super, arguments, NewTarget);
      } else {
        result = Super.apply(this, arguments);
      }
      return _possibleConstructorReturn3(this, result);
    };
  }
  function _possibleConstructorReturn3(self, call) {
    if (call && (_typeof4(call) === "object" || typeof call === "function")) {
      return call;
    } else if (call !== void 0) {
      throw new TypeError("Derived constructors may only return object or undefined");
    }
    return _assertThisInitialized3(self);
  }
  function _assertThisInitialized3(self) {
    if (self === void 0) {
      throw new ReferenceError("this hasn't been initialised - super() hasn't been called");
    }
    return self;
  }
  function _wrapNativeSuper3(Class) {
    var _cache = typeof Map === "function" ? /* @__PURE__ */ new Map() : void 0;
    _wrapNativeSuper3 = function _wrapNativeSuper6(Class2) {
      if (Class2 === null || !_isNativeFunction3(Class2)) return Class2;
      if (typeof Class2 !== "function") {
        throw new TypeError("Super expression must either be null or a function");
      }
      if (typeof _cache !== "undefined") {
        if (_cache.has(Class2)) return _cache.get(Class2);
        _cache.set(Class2, Wrapper);
      }
      function Wrapper() {
        return _construct3(Class2, arguments, _getPrototypeOf3(this).constructor);
      }
      Wrapper.prototype = Object.create(Class2.prototype, { constructor: { value: Wrapper, enumerable: false, writable: true, configurable: true } });
      return _setPrototypeOf3(Wrapper, Class2);
    };
    return _wrapNativeSuper3(Class);
  }
  function _construct3(Parent, args, Class) {
    if (_isNativeReflectConstruct3()) {
      _construct3 = Reflect.construct.bind();
    } else {
      _construct3 = function _construct6(Parent2, args2, Class2) {
        var a = [null];
        a.push.apply(a, args2);
        var Constructor = Function.bind.apply(Parent2, a);
        var instance = new Constructor();
        if (Class2) _setPrototypeOf3(instance, Class2.prototype);
        return instance;
      };
    }
    return _construct3.apply(null, arguments);
  }
  function _isNativeReflectConstruct3() {
    if (typeof Reflect === "undefined" || !Reflect.construct) return false;
    if (Reflect.construct.sham) return false;
    if (typeof Proxy === "function") return true;
    try {
      Boolean.prototype.valueOf.call(Reflect.construct(Boolean, [], function() {
      }));
      return true;
    } catch (e) {
      return false;
    }
  }
  function _isNativeFunction3(fn) {
    return Function.toString.call(fn).indexOf("[native code]") !== -1;
  }
  function _setPrototypeOf3(o, p) {
    _setPrototypeOf3 = Object.setPrototypeOf ? Object.setPrototypeOf.bind() : function _setPrototypeOf6(o2, p2) {
      o2.__proto__ = p2;
      return o2;
    };
    return _setPrototypeOf3(o, p);
  }
  function _getPrototypeOf3(o) {
    _getPrototypeOf3 = Object.setPrototypeOf ? Object.getPrototypeOf.bind() : function _getPrototypeOf6(o2) {
      return o2.__proto__ || Object.getPrototypeOf(o2);
    };
    return _getPrototypeOf3(o);
  }
  var MESSAGES = {
    XLS_FILE_NOT_SUPPORTED: "You passed a legacy `.xls` file. Only `.xlsx` files are supported",
    FILE_NOT_SUPPORTED: "Doesn't look like an `.xlsx` file",
    INVALID_ZIP: "Couldn't unzip `.xlsx` file contents",
    NO_DATA: "No data"
  };
  var InvalidInputError = /* @__PURE__ */ function(_Error) {
    _inherits3(InvalidInputError2, _Error);
    var _super = _createSuper3(InvalidInputError2);
    function InvalidInputError2(code, cause) {
      var _this;
      _classCallCheck3(this, InvalidInputError2);
      _this = _super.call(this, MESSAGES[code] || code);
      _this.code = code;
      _this.name = "InvalidInputError";
      _this.cause = cause;
      return _this;
    }
    return _createClass3(InvalidInputError2);
  }(/* @__PURE__ */ _wrapNativeSuper3(Error));

  // node_modules/read-excel-file/modules/export/filterZipArchiveEntry.js
  function filterZipArchiveEntry(_ref) {
    var path = _ref.path;
    return path.endsWith(".xml") || path.endsWith(".xml.rels");
  }

  // node_modules/read-excel-file/modules/xlsx/file/createFileTypeDetector.js
  var ZIP_FILE_SIGNATURE = [80, 75];
  var XLS_FILE_SIGNATURE = [208, 207, 17, 224];
  var FILE_TYPE_SIGNATURES = [ZIP_FILE_SIGNATURE, XLS_FILE_SIGNATURE];
  var XLSX_FILE_TYPE = FILE_TYPE_SIGNATURES.indexOf(ZIP_FILE_SIGNATURE);
  var XLS_FILE_TYPE = FILE_TYPE_SIGNATURES.indexOf(XLS_FILE_SIGNATURE);
  function createFileTypeDetector() {
    var type;
    var possibleTypes = indexesOf(FILE_TYPE_SIGNATURES);
    var i = 0;
    return function(_byte) {
      if (isNaN(type)) {
        var t;
        possibleTypes = possibleTypes.filter(function(typeIndex) {
          if (_byte === FILE_TYPE_SIGNATURES[typeIndex][i]) {
            if (FILE_TYPE_SIGNATURES[typeIndex].length === i + 1) {
              t = typeIndex;
            }
            return true;
          }
        });
        if (possibleTypes.length === 1) {
          type = t;
        } else if (possibleTypes.length === 0) {
          type = -1;
        }
      }
      i++;
      return type;
    };
  }
  function indexesOf(array) {
    var indexes = [];
    var i = 0;
    while (i < array.length) {
      indexes.push(i);
      i++;
    }
    return indexes;
  }

  // node_modules/read-excel-file/modules/xlsx/file/validateLeadingBytes.js
  function _createForOfIteratorHelperLoose(o, allowArrayLike) {
    var it = typeof Symbol !== "undefined" && o[Symbol.iterator] || o["@@iterator"];
    if (it) return (it = it.call(o)).next.bind(it);
    if (Array.isArray(o) || (it = _unsupportedIterableToArray(o)) || allowArrayLike && o && typeof o.length === "number") {
      if (it) o = it;
      var i = 0;
      return function() {
        if (i >= o.length) return { done: true };
        return { done: false, value: o[i++] };
      };
    }
    throw new TypeError("Invalid attempt to iterate non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _unsupportedIterableToArray(o, minLen) {
    if (!o) return;
    if (typeof o === "string") return _arrayLikeToArray(o, minLen);
    var n = Object.prototype.toString.call(o).slice(8, -1);
    if (n === "Object" && o.constructor) n = o.constructor.name;
    if (n === "Map" || n === "Set") return Array.from(o);
    if (n === "Arguments" || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(n)) return _arrayLikeToArray(o, minLen);
  }
  function _arrayLikeToArray(arr, len) {
    if (len == null || len > arr.length) len = arr.length;
    for (var i = 0, arr2 = new Array(len); i < len; i++) arr2[i] = arr[i];
    return arr2;
  }
  function validateLeadingBytes(bytes) {
    var fileTypeDetector = createFileTypeDetector();
    for (var _iterator = _createForOfIteratorHelperLoose(bytes), _step; !(_step = _iterator()).done; ) {
      var _byte = _step.value;
      if (validateByte(_byte, fileTypeDetector)) {
        return;
      }
    }
    noFileTypeCouldBeDetermined(bytes.length);
  }
  function validateByte(_byte2, fileTypeDetector) {
    var fileType = fileTypeDetector(_byte2);
    if (fileType !== void 0) {
      if (fileType === XLS_FILE_TYPE) {
        throw new InvalidInputError("XLS_FILE_NOT_SUPPORTED");
      }
      if (fileType < 0) {
        throw new InvalidInputError("FILE_NOT_SUPPORTED");
      }
      return true;
    }
  }
  function noFileTypeCouldBeDetermined(byteCount) {
    throw new InvalidInputError(byteCount === 0 ? "NO_DATA" : "FILE_NOT_SUPPORTED");
  }

  // node_modules/read-excel-file/modules/utility/checkpoint.js
  var latestCheckpointTimestamp;
  function checkpoint(name) {
    var now = Date.now();
    var shouldOutputLog = typeof global !== "undefined" ? Boolean(global.READ_EXCEL_FILE_CHECKPOINTS) : typeof window !== "undefined" ? Boolean(window.READ_EXCEL_FILE_CHECKPOINTS) : false;
    if (shouldOutputLog) {
      if (latestCheckpointTimestamp) {
        console.log("  -", now - latestCheckpointTimestamp, "ms");
      }
      console.log("*", name);
    }
    latestCheckpointTimestamp = now;
  }
  function resetCheckpoint() {
    latestCheckpointTimestamp = void 0;
  }

  // node_modules/read-excel-file/modules/export/unpackXlsxFileBrowser.js
  function unpackXlsxFile(input) {
    resetCheckpoint();
    checkpoint("unpack files");
    if (input instanceof File || input instanceof Blob) {
      return input.arrayBuffer().then(getResultFromArrayBuffer);
    }
    return Promise.resolve(input).then(getResultFromArrayBuffer);
  }
  function getResultFromArrayBuffer(arrayBuffer) {
    validateLeadingBytes(new Uint8Array(arrayBuffer));
    return unzipFromArrayBuffer(arrayBuffer, {
      filter: filterZipArchiveEntry
    }).then(function(result) {
      return result;
    }, function(error) {
      if (error instanceof UnzipError) {
        throw new InvalidInputError("INVALID_ZIP", error.cause);
      } else {
        throw error;
      }
    });
  }

  // node_modules/read-excel-file/modules/xlsx/parseSpreadsheetInfo.js
  function parseSpreadsheetInfo(content, parseXml2) {
    var state = createInitialState();
    return parseXml2(content, state, onOpenTag, null, null).then(function() {
      return getResultFromState(state);
    });
    function createInitialState() {
      return {
        workbookPr: void 0,
        sheets: []
      };
    }
    function getResultFromState(state2) {
      return {
        epoch1904: state2.workbookPr ? state2.workbookPr.epoch1904 : false,
        sheets: state2.sheets
      };
    }
    function onOpenTag(tagName, attributes, state2) {
      if (tagName === "workbookPr") {
        if (!state2.workbookPr) {
          state2.workbookPr = {
            epoch1904: attributes.date1904 === "1"
          };
        }
      } else if (tagName === "sheet") {
        if (attributes.name) {
          state2.sheets.push({
            // `sheetId` attribute value is an arbitrary, `1`-based unique positive integer
            // assigned to a worksheet, typically starting at `1` for the first sheet.
            //  Deleting and adding new sheets might cause the sheetId values to become non-sequential.
            // For example, `sheetId`s could be `1`, `2`, `4`, if sheet `3` was deleted.
            id: Number(attributes.sheetId),
            name: attributes.name,
            relationId: attributes.id
          });
        }
      }
    }
  }

  // node_modules/read-excel-file/modules/xlsx/parseFilePaths.js
  function parseFilePaths(content, parseXml2) {
    var RELATIONSHIPS_BASE_URL_TRANSITIONAL_STANDARD = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/";
    var RELATIONSHIPS_BASE_URL_STRICT_STANDARD = "http://purl.oclc.org/ooxml/officeDocument/relationships/";
    var state = createInitialState();
    return parseXml2(content, state, onOpenTag, null, null).then(function() {
      return getResultFromState(state);
    });
    function createInitialState() {
      return {
        sheets: {},
        sharedStrings: void 0,
        styles: void 0
      };
    }
    function getResultFromState(state2) {
      return state2;
    }
    function onOpenTag(tagName, attributes, state2) {
      if (tagName === "Relationship") {
        addFilePathForRelation(state2, attributes.Id, attributes.Type, attributes.Target);
      }
    }
    function addFilePathForRelation(state2, id, type, target) {
      switch (type) {
        case RELATIONSHIPS_BASE_URL_TRANSITIONAL_STANDARD + "styles":
        case RELATIONSHIPS_BASE_URL_STRICT_STANDARD + "styles":
          state2.styles = getFilePathFromRelationTarget(target);
          break;
        case RELATIONSHIPS_BASE_URL_TRANSITIONAL_STANDARD + "sharedStrings":
        case RELATIONSHIPS_BASE_URL_STRICT_STANDARD + "sharedStrings":
          state2.sharedStrings = getFilePathFromRelationTarget(target);
          break;
        case RELATIONSHIPS_BASE_URL_TRANSITIONAL_STANDARD + "worksheet":
        case RELATIONSHIPS_BASE_URL_STRICT_STANDARD + "worksheet":
          state2.sheets[id] = getFilePathFromRelationTarget(target);
          break;
      }
    }
    function getFilePathFromRelationTarget(path) {
      if (path[0] === "/") {
        return path.slice("/".length);
      }
      return "xl/" + path;
    }
  }

  // node_modules/read-excel-file/modules/xlsx/parseStyles.js
  function _typeof5(o) {
    "@babel/helpers - typeof";
    return _typeof5 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof5(o);
  }
  var _excluded = ["xfId"];
  function ownKeys(e, r) {
    var t = Object.keys(e);
    if (Object.getOwnPropertySymbols) {
      var o = Object.getOwnPropertySymbols(e);
      r && (o = o.filter(function(r2) {
        return Object.getOwnPropertyDescriptor(e, r2).enumerable;
      })), t.push.apply(t, o);
    }
    return t;
  }
  function _objectSpread(e) {
    for (var r = 1; r < arguments.length; r++) {
      var t = null != arguments[r] ? arguments[r] : {};
      r % 2 ? ownKeys(Object(t), true).forEach(function(r2) {
        _defineProperty(e, r2, t[r2]);
      }) : Object.getOwnPropertyDescriptors ? Object.defineProperties(e, Object.getOwnPropertyDescriptors(t)) : ownKeys(Object(t)).forEach(function(r2) {
        Object.defineProperty(e, r2, Object.getOwnPropertyDescriptor(t, r2));
      });
    }
    return e;
  }
  function _defineProperty(obj, key2, value) {
    key2 = _toPropertyKey4(key2);
    if (key2 in obj) {
      Object.defineProperty(obj, key2, { value, enumerable: true, configurable: true, writable: true });
    } else {
      obj[key2] = value;
    }
    return obj;
  }
  function _toPropertyKey4(arg) {
    var key2 = _toPrimitive4(arg, "string");
    return _typeof5(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive4(input, hint) {
    if (_typeof5(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof5(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _objectWithoutProperties(source, excluded) {
    if (source == null) return {};
    var target = _objectWithoutPropertiesLoose(source, excluded);
    var key2, i;
    if (Object.getOwnPropertySymbols) {
      var sourceSymbolKeys = Object.getOwnPropertySymbols(source);
      for (i = 0; i < sourceSymbolKeys.length; i++) {
        key2 = sourceSymbolKeys[i];
        if (excluded.indexOf(key2) >= 0) continue;
        if (!Object.prototype.propertyIsEnumerable.call(source, key2)) continue;
        target[key2] = source[key2];
      }
    }
    return target;
  }
  function _objectWithoutPropertiesLoose(source, excluded) {
    if (source == null) return {};
    var target = {};
    var sourceKeys = Object.keys(source);
    var key2, i;
    for (i = 0; i < sourceKeys.length; i++) {
      key2 = sourceKeys[i];
      if (excluded.indexOf(key2) >= 0) continue;
      target[key2] = source[key2];
    }
    return target;
  }
  function parseStyles(content, parseXml2) {
    var state = createInitialState();
    return parseXml2(content, state, onOpenTag, onCloseTag, null).then(function() {
      return getResultFromState(state);
    });
    function createInitialState() {
      return {
        numberFormats: [],
        baseStyles: [],
        // The first `164` elements of the `styles` array are going to be `undefined`
        // because those represent the built-in "default" styles in `.xlsx` specification.
        // These "default" styles have IDs from `0` to `163`, i.e. according to their index.
        styles: [],
        cellStyleXfs: false,
        cellXfs: false
      };
    }
    function getResultFromState(state2) {
      return state2.styles.map(function(style) {
        if (style.xfId) {
          var xfId = style.xfId, styleProperties = _objectWithoutProperties(style, _excluded);
          return _objectSpread(_objectSpread({}, state2.baseStyles[xfId]), styleProperties);
        } else {
          return style;
        }
      });
    }
    function onOpenTag(tagName, attributes, state2) {
      if (tagName === "numFmt") {
        var numFmtId = Number(attributes.numFmtId);
        var numberFormat = {
          id: numFmtId
        };
        if (numFmtId >= 100) {
          numberFormat.template = attributes.formatCode;
        }
        state2.numberFormats[numFmtId] = numberFormat;
      } else if (tagName === "cellStyleXfs") {
        state2.cellStyleXfs = true;
      } else if (tagName === "cellXfs") {
        state2.cellXfs = true;
      } else if (tagName === "xf") {
        if (state2.cellStyleXfs) {
          state2.baseStyles.push(parseCellStyle(attributes));
        } else if (state2.cellXfs) {
          var style = parseCellStyle(attributes, state2.numberFormats);
          if (attributes.xfId) {
            style.xfId = Number(attributes.xfId);
          }
          state2.styles.push(style);
        }
      }
    }
    function onCloseTag(tagName, state2) {
      if (tagName === "cellStyleXfs") {
        state2.cellStyleXfs = false;
      } else if (tagName === "cellXfs") {
        state2.cellXfs = false;
      }
    }
    function parseCellStyle(attributes, numberFormats) {
      var style = {};
      if (attributes.numFmtId) {
        var numFmtId = Number(attributes.numFmtId);
        if (numberFormats && numberFormats[numFmtId]) {
          style.numberFormat = numberFormats[numFmtId];
        } else {
          style.numberFormat = {
            id: numFmtId
          };
        }
      }
      return style;
    }
  }

  // node_modules/read-excel-file/modules/xlsx/parseSharedStrings.js
  function parseSharedStrings(content, parseXml2) {
    var state = createInitialState();
    return parseXml2(content, state, onOpenTag, onCloseTag, onText).then(function() {
      return getResultFromState(state);
    });
    function createInitialState() {
      return {
        si: void 0,
        strings: []
      };
    }
    function getResultFromState(state2) {
      return state2.strings;
    }
    function onOpenTag(tagName, attributes, state2) {
      if (tagName === "si") {
        state2.si = createInitialStateInSharedString();
      } else if (state2.si) {
        onOpenTagInSharedString(tagName, attributes, state2.si);
      }
    }
    function onCloseTag(tagName, state2) {
      if (tagName === "si") {
        state2.strings.push(state2.si.string);
        state2.si = void 0;
      } else if (state2.si) {
        onCloseTagInSharedString(tagName, state2.si);
      }
    }
    function onText(text2, state2) {
      if (state2.si) {
        onTextInSharedString(text2, state2.si);
      }
    }
    function createInitialStateInSharedString() {
      return {
        t: false,
        r: false,
        rPh: false,
        string: ""
      };
    }
    function onOpenTagInSharedString(tagName, attributes, state2) {
      if (tagName === "t") {
        state2.t = true;
      } else if (tagName === "r") {
        state2.r = true;
      } else if (tagName === "rPh") {
        state2.rPh = true;
      }
    }
    function onCloseTagInSharedString(tagName, state2) {
      if (tagName === "t") {
        state2.t = false;
      } else if (tagName === "r") {
        state2.r = false;
      } else if (tagName === "rPh") {
        state2.rPh = false;
      }
    }
    function onTextInSharedString(text2, state2) {
      if (state2.rPh) {
      } else if (state2.t) {
        if (state2.r) {
          state2.string += text2;
        } else {
          state2.string = text2;
        }
      }
    }
  }

  // node_modules/read-excel-file/modules/xlsx/parseExcelTimestamp.js
  function parseExcelTimestamp(excelSerialDate, epoch1904) {
    var NUMBER_OF_LEAP_YEARS_BETWEEN_1900_AND_1970 = 17;
    var JANUARY_0TH_1900_DAY = 1;
    var ERRONEOUS_FEBRUARY_29_1990_DAY = 1;
    var DAY = 24 * 60 * 60 * 1e3;
    var DAYS_IN_YEAR = 365;
    if (epoch1904) {
      excelSerialDate += (1904 - 1900) * DAYS_IN_YEAR + JANUARY_0TH_1900_DAY + ERRONEOUS_FEBRUARY_29_1990_DAY;
    }
    var daysBeforeUnixEpoch = JANUARY_0TH_1900_DAY + ERRONEOUS_FEBRUARY_29_1990_DAY + (1970 - 1900) * DAYS_IN_YEAR + NUMBER_OF_LEAP_YEARS_BETWEEN_1900_AND_1970;
    return Math.floor((excelSerialDate - daysBeforeUnixEpoch) * DAY);
  }

  // node_modules/read-excel-file/modules/xlsx/isDateFormat.js
  var DATE_FORMAT_POSTFIX_THAT_ALLOWS_ANY_ARBITRARY_TEXT_INPUT = /;@$/;
  var DATE_FORMAT_TOKEN_SPLITTER_REG_EXP = /[^a-z0#\?%]+/;
  function isDateFormat(formatId, template, dateFormatDetectionCache) {
    var cachedResult = dateFormatDetectionCache[formatId];
    if (cachedResult === void 0) {
      return dateFormatDetectionCache[formatId] = isDateFormatTemplate(template);
    }
    return cachedResult;
  }
  function isDateFormatTemplate(template) {
    template = template.toLowerCase();
    template = template.replace(DATE_FORMAT_POSTFIX_THAT_ALLOWS_ANY_ARBITRARY_TEXT_INPUT, "");
    template = template.replace(/\\./g, " ");
    var templates = template.split(";");
    return templates.some(isDateFormatSubTemplate);
    function isDateFormatSubTemplate(template2) {
      template2 = template2.replace(/"[^"]*"/g, " ");
      template2 = template2.replace(/(\[?[smhd]{1,2}\]?)[\.\,]00?0?/g, "$1");
      template2 = template2.replace(/\[([smhd]{1,2})\]/g, "$1");
      template2 = template2.replace(/\[[^\]]*\]/g, "");
      var tokens = template2.split(DATE_FORMAT_TOKEN_SPLITTER_REG_EXP).filter(function(_) {
        return _;
      });
      return tokens.length === 0 ? false : tokens.every(function(token) {
        return DATE_FORMAT_TEMPLATE_TOKENS.indexOf(token) >= 0;
      });
    }
  }
  var DATE_FORMAT_TEMPLATE_TOKENS = [
    // Seconds.
    "s",
    // Seconds (min two digits). Example: "05".
    "ss",
    // Minutes. Could also means months, depending on the context. Example: "5".
    "m",
    // Minutes (min two digits). Could also means months, depending on the context. Example: "05".
    "mm",
    // Hours. Example: "1".
    "h",
    // Hours (min two digits). Example: "01".
    "hh",
    // "am" part of "am/pm" or "AM/PM".
    "am",
    // "pm" part of "am/pm" or "AM/PM".
    "pm",
    // "a" part of "a/p" or "A/P".
    "a",
    // "p" part of "a/p" or "A/P".
    "p",
    // Day. Example: "1"
    "d",
    // Day (min two digits). Example: "01"
    "dd",
    // Short, three-letter abbreviation for the day of the week. Example: "Mon"
    "ddd",
    // Full name of the day of the week. Example: "Monday"
    "dddd",
    // Abbreviated weekday name. Example: "Mon", "Tue"
    "aaa",
    // Full weekday name. Example: "Monday", "Tuesday"
    "aaaa",
    // First letter of the weekday name. Example: "M", "T"
    "aaaaa",
    // Month (numeric). Could also mean minutes, depending on the context. Example: "1".
    "m",
    // Month (numeric, min two digits). Could also mean minutes, depending on the context. Example: "01".
    "mm",
    // Month (shortened month name). Example: "Jan".
    "mmm",
    // Month (full month name). Example: "January".
    "mmmm",
    // Month (first letter). Example: "J".
    "mmmmm",
    // Excel typically treats a single `y` the same as `yy` in custom formatting,
    // though standard practice is using `yy` or `yyyy`.
    "y",
    // Two-digit year, with a leading zero if needed. Example: "01".
    "yy",
    // Full year. Example: "2001".
    "yyyy",
    //
    // `e` or `ee` token stands for "era year" and represents an era-based year,
    // primarily supporting Japanese, Taiwanese, or Minguo/Buddhist calendar systems.
    // An example of an "era year" would be Heisei or Reiwa year numbers,
    // or Buddhist Era years which are Gregorian years plus 543.
    //
    // * `e` outputs the era year as a 4-digit (or unpadded) number.
    //   For example, `2026` or `115` depending on the active regional era.
    //   In standard Western/Gregorian locales, Excel often falls back to treating `e`
    //   similarly to a regular year layout.
    //
    // * `ee` outputs a 2-digit padded era year. For example, `26` or `15`.
    //
    // * `eeee` — While `yyyy` is the standard token for a 4-digit year,
    //   `eeee` is historically used for specialized international calendar eras
    //   (like the Japanese or Taiwanese imperial eras). However, in standard
    //   Western/Gregorian system locales, Excel treats `eeee` exactly like `yyyy`.
    "e",
    "ee",
    "eeee"
  ];

  // node_modules/read-excel-file/modules/xlsx/isDateFormatStyle.js
  function isDateFormatStyle(style, defaultDateFormat, shouldGuessDateFormatFromNumberFormatTemplate, dateFormatDetectionCache) {
    if (!style.numberFormat) {
      return false;
    }
    if (
      // Whether it's a "number format" that's conventionally used for storing date timestamps.
      BUILT_IN_DATE_FORMAT_IDS.indexOf(style.numberFormat.id) >= 0 || // Whether it's a "number format" that uses a "formatting template"
      // that the developer is certain is a date formatting template.
      defaultDateFormat && style.numberFormat.template === defaultDateFormat || // Whether the "smart formatting template" feature is not disabled
      // and it has detected that it's a date formatting template by looking at it.
      shouldGuessDateFormatFromNumberFormatTemplate && style.numberFormat.template && isDateFormat(style.numberFormat.id, style.numberFormat.template, dateFormatDetectionCache)
    ) {
      return true;
    }
    return false;
  }
  var LOCALE_INDEPENDENT_BUILT_IN_DATE_FORMAT_IDS = [
    14,
    // mm-dd-yy
    15,
    // d-mmm-yy
    16,
    // d-mmm
    17,
    // mmm-yy
    18,
    // h:mm AM/PM
    19,
    // h:mm:ss AM/PM
    20,
    // h:mm
    21,
    // h:mm:ss
    22,
    // m/d/yy h:mm
    45,
    // mm:ss
    46,
    // [h]:mm:ss
    47
    // mmss.0
  ];
  var MAINLAND_CHINESE_OR_TAIWANESE_LOCALE_BUILT_IN_DATE_FORMAT_IDS = [
    27,
    // [$-404]e/m/d OR yyyy"年"m"月"
    28,
    // [$-404]e"年"m"月"d"日" OR m"月"d"日"
    29,
    // [$-404]e"年"m"月"d"日" OR m"月"d"日"
    30,
    // m/d/yy OR m-d-yy
    31,
    // yyyy"年"m"月"d"日" OR yyyy"年"m"月"d"日"
    32,
    // hh"時"mm"分" OR h"时"mm"分"
    33,
    // hh"時"mm"分"ss"秒" OR h"时"mm"分"ss"秒"
    34,
    // 上午/下午hh"時"mm"分" OR 上午/下午h"时"mm"分"
    35,
    // 上午/下午hh"時"mm"分"ss"秒" OR 上午/下午h"时"mm"分"ss"秒"
    36,
    // [$-404]e/m/d OR yyyy"年"m"月"
    50,
    // [$-404]e/m/d OR yyyy"年"m"月"
    51,
    // [$-404]e"年"m"月"d"日" OR m"月"d"日"
    52,
    // 上午/下午hh"時"mm"分" OR yyyy"年"m"月"
    53,
    // 上午/下午hh"時"mm"分"ss"秒" OR m"月"d"日"
    54,
    // [$-404]e"年"m"月"d"日" OR m"月"d"日"
    55,
    // 上午/下午hh"時"mm"分" OR 上午/下午h"时"mm"分"
    56,
    // 上午/下午hh"時"mm"分"ss"秒" OR 上午/下午h"时"mm"分"ss"秒"
    57,
    // [$-404]e/m/d OR yyyy"年"m"月"
    58
    // [$-404]e"年"m"月"d"日" OR m"月"d"日"
  ];
  var JAPANESE_OR_KOREAN_LOCALE_BUILT_IN_DATE_FORMAT_IDS = [
    27,
    // [$-411]ge.m.d OR yyyy"年" mm"月" dd"日"
    28,
    // [$-411]ggge"年"m"月"d"日" OR mm-dd
    29,
    // [$-411]ggge"年"m"月"d"日" OR mm-dd
    30,
    // m/d/yy OR mm-dd-yy
    31,
    // yyyy"年"m"月"d"日" OR yyyy"년" mm"월" dd"일"
    32,
    // h"時"mm"分" OR h"시" mm"분"
    33,
    // h"時"mm"分"ss"秒" OR h"시" mm"분" ss"초"
    34,
    // yyyy"年"m"月" OR yyyy-mm-dd
    35,
    // m"月"d"日" OR yyyy-mm-dd
    36,
    // [$-411]ge.m.d OR yyyy"年" mm"月" dd"日"
    50,
    // [$-411]ge.m.d OR yyyy"年" mm"月" dd"日"
    51,
    // [$-411]ggge"年"m"月"d"日" OR mm-dd
    52,
    // yyyy"年"m"月" OR yyyy-mm-dd
    53,
    // m"月"d"日" OR yyyy-mm-dd
    54,
    // [$-411]ggge"年"m"月"d"日" OR mm-dd
    55,
    // yyyy"年"m"月" OR yyyy-mm-dd
    56,
    // m"月"d"日" OR yyyy-mm-dd
    57,
    // [$-411]ge.m.d OR yyyy"年" mm"月" dd"日"
    58
    // [$-411]ggge"年"m"月"d"日" OR mm-dd
  ];
  var THAI_LOCALE_BUILT_IN_DATE_FORMAT_IDS = [
    71,
    // ว/ด/ปปปป
    72,
    // ว-ดดด-ปป
    73,
    // ว-ดดด
    74,
    // ดดด-ปป
    75,
    // ช:นน
    76,
    // ช:นน:ทท
    77,
    // ว/ด/ปปปป ช:นน
    78,
    // นน:ทท
    79,
    // [ช]:นน:ทท
    80,
    // นน:ทท.0
    81
    // d/m/bb
  ];
  var BUILT_IN_DATE_FORMAT_IDS = LOCALE_INDEPENDENT_BUILT_IN_DATE_FORMAT_IDS.concat(
    // Add Mainland Chinese or Taiwanese date format IDs that haven't already been added.
    MAINLAND_CHINESE_OR_TAIWANESE_LOCALE_BUILT_IN_DATE_FORMAT_IDS
  ).concat(
    // Add Japanese or Korean date format IDs that haven't already been added.
    JAPANESE_OR_KOREAN_LOCALE_BUILT_IN_DATE_FORMAT_IDS.filter(function(numberFormatId) {
      return MAINLAND_CHINESE_OR_TAIWANESE_LOCALE_BUILT_IN_DATE_FORMAT_IDS.indexOf(numberFormatId) < 0;
    })
  ).concat(
    // Add Thai date format IDs that haven't already been added.
    THAI_LOCALE_BUILT_IN_DATE_FORMAT_IDS.filter(function(numberFormatId) {
      return MAINLAND_CHINESE_OR_TAIWANESE_LOCALE_BUILT_IN_DATE_FORMAT_IDS.indexOf(numberFormatId) < 0;
    }).filter(function(numberFormatId) {
      return JAPANESE_OR_KOREAN_LOCALE_BUILT_IN_DATE_FORMAT_IDS.indexOf(numberFormatId) < 0;
    })
  );

  // node_modules/read-excel-file/modules/xlsx/parseCell.js
  function _slicedToArray(arr, i) {
    return _arrayWithHoles(arr) || _iterableToArrayLimit(arr, i) || _unsupportedIterableToArray2(arr, i) || _nonIterableRest();
  }
  function _nonIterableRest() {
    throw new TypeError("Invalid attempt to destructure non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _unsupportedIterableToArray2(o, minLen) {
    if (!o) return;
    if (typeof o === "string") return _arrayLikeToArray2(o, minLen);
    var n = Object.prototype.toString.call(o).slice(8, -1);
    if (n === "Object" && o.constructor) n = o.constructor.name;
    if (n === "Map" || n === "Set") return Array.from(o);
    if (n === "Arguments" || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(n)) return _arrayLikeToArray2(o, minLen);
  }
  function _arrayLikeToArray2(arr, len) {
    if (len == null || len > arr.length) len = arr.length;
    for (var i = 0, arr2 = new Array(len); i < len; i++) arr2[i] = arr[i];
    return arr2;
  }
  function _iterableToArrayLimit(r, l) {
    var t = null == r ? null : "undefined" != typeof Symbol && r[Symbol.iterator] || r["@@iterator"];
    if (null != t) {
      var e, n, i, u, a = [], f = true, o = false;
      try {
        if (i = (t = t.call(r)).next, 0 === l) {
          if (Object(t) !== t) return;
          f = false;
        } else for (; !(f = (e = i.call(t)).done) && (a.push(e.value), a.length !== l); f = true) ;
      } catch (r2) {
        o = true, n = r2;
      } finally {
        try {
          if (!f && null != t["return"] && (u = t["return"](), Object(u) !== u)) return;
        } finally {
          if (o) throw n;
        }
      }
      return a;
    }
  }
  function _arrayWithHoles(arr) {
    if (Array.isArray(arr)) return arr;
  }
  var EMPTY_CELL_VALUE = null;
  var EMPTY_CELL = [null, EMPTY_CELL_VALUE];
  function parseCell(t, s, v, inlineString, _ref) {
    var _ref2 = _slicedToArray(_ref, 7), sharedStrings = _ref2[0], styles = _ref2[1], epoch1904 = _ref2[2], dateFormatDetectionCache = _ref2[3], defaultDateFormat = _ref2[4], dateTemplateParser = _ref2[5], parseNumberCustom = _ref2[6];
    switch (t || "n") {
      // `t="str"` means that the cell value is calculated using a formula.
      // The formula is defined as the text of a child `<f/>` element.
      //
      // It could optionally include a `<v/>` element whose text is the cached result
      // of the calculation from the last time the file was saved in a spreadsheet editor application.
      //
      // An optional `<v/>` element holds a pre-computed result of the formula defined by `<f/>`.
      //
      // Example:
      //
      // <c r="B3" t="str">
      // 	<f>CONCATENATE(C1,D1)</f>
      // 	<v>C1ValueD1Value</v>
      // </c>
      //
      // Here's a guide on formulas in XLSX files:
      // https://github.com/MiniMax-AI/skills/blob/main/skills/minimax-xlsx/references/validate.md
      //
      case "str":
        if (v === void 0) {
          return "VALUE_MISSING";
        }
        if (!v) {
          return EMPTY_CELL;
        }
        return ["s", v];
      // `t="inlineStr"` means that `<is/>` holds the string value.
      //
      // Inside a `<c t="inlineStr"/>`, the specification requires there to exist an `<is/>` element,
      // and within that `<is/>` element it requires to exist a `<t/>` element.
      //
      // Example:
      //
      // <c r="A1" s="1" t="inlineStr">
      //   <is>
      //     <t>
      //       Test 123
      //     </t>
      //   </is>
      // </c>
      //
      case "inlineStr":
        if (inlineString === void 0) {
          return "VALUE_MISSING";
        }
        return ["s", inlineString];
      // `type="s"` means that the string value is stored in the Shared Strings Table.
      // This way it attempts to compress the `.xlsx` file by reusing all string values
      // in case they repeat throughout the spreadsheet.
      //
      // This optimization can't be used when writing an `.xlsx` file in a "streaming"
      // fashion, i.e. when the entire spreadsheet data is not known in adavance
      // at the start of writing the file.
      // But it can be used in all other situations. And hence, it is used.
      // So this is the most common cell type, actually.
      //
      // Example:
      //
      // <c r="A3" t="s">
      //   <v>3</v>
      // </c>
      //
      case "s":
        if (!v) {
          return "VALUE_MISSING";
        }
        var sharedStringIndex = Number(v);
        if (isNaN(sharedStringIndex) || sharedStrings[sharedStringIndex] === void 0) {
          return "VALUE_INVALID";
        }
        return ["s", sharedStrings[sharedStringIndex]];
      // Boolean (TRUE/FALSE) values are stored as either "1" or "0" in cells of type "b".
      //
      // Example:
      //
      // <c r="A1" t="b">
      //   <v>1</v>
      // </c>
      //
      case "b":
        if (!v) {
          return "VALUE_MISSING";
        }
        if (v === "1") {
          return ["b", true];
        }
        if (v === "0") {
          return ["b", false];
        }
        return "VALUE_INVALID";
      // If cell type is "e", the `<v/>` element's text is an error code string (required).
      //
      // Example:
      //
      // <c r="A1" t="e">
      //   <f>1/0</f>
      //   <v>#DIV/0!</v>
      // </c>
      //
      case "e":
        if (!v) {
          return "VALUE_MISSING";
        }
        return ["e", v];
      // XLSX supports date cells of type "d", though it seems like it (almost?) never
      // uses type "d" for storing dates, preferring type "n" and numeric timestamp instead.
      // The value of a "d" cell is supposedly a string in "ISO 8601" format.
      // I haven't seen an `.xlsx` file having such cells.
      //
      // Example:
      //
      // <c r="A1" s="1" t="d">
      //   <v>
      //     2021-06-10T00:47:45.700Z
      //   </v>
      // </c>
      //
      case "d":
        if (!v) {
          return EMPTY_CELL;
        }
        var parsedDate = new Date(v);
        if (isNaN(parsedDate.valueOf())) {
          return "VALUE_INVALID";
        }
        return ["d", parsedDate.getTime()];
      // type "n" is used for numeric cells.
      //
      // An optional `s` attribute defines how this number should be formatted — 
      // it should be a zero-based index of the style (XF record) in `styles.xml`.
      //
      // Example:
      //
      // <c r="A1" s="1" t="n">
      //   <v>123.45</v>
      // </c>
      //
      case "n":
        if (!v) {
          return EMPTY_CELL;
        }
        if (s) {
          var styleId = Number(s);
          if (isNaN(styleId) || styles[styleId] === void 0) {
            return "FORMAT_INVALID";
          }
          if (isDateFormatStyle(styles[styleId], defaultDateFormat, dateTemplateParser, dateFormatDetectionCache)) {
            var timestamp = Number(v);
            if (isNaN(timestamp)) {
              return "VALUE_INVALID";
            }
            return ["d", parseExcelTimestamp(timestamp, epoch1904)];
          }
        }
        if (parseNumberCustom) {
          return ["n", v];
        }
        var number = Number(v);
        if (isNaN(number)) {
          return "VALUE_INVALID";
        }
        return ["n", number];
      default:
        return "TYPE_INVALID";
    }
  }

  // node_modules/read-excel-file/modules/xlsx/parseCellAddress.js
  function parseCellAddress(cellAddress) {
    var columnNumber = 0;
    var i = 0;
    while (i < cellAddress.length) {
      var charCode = cellAddress.charCodeAt(i);
      if (charCode >= 48 && charCode <= 57) {
        var rowNumber = Number(cellAddress.slice(i));
        if (isNaN(rowNumber)) {
          invalidCellAddress(cellAddress);
        }
        return [
          // Row number (starting at `1`).
          rowNumber,
          // Column number (starting at `1`).
          columnNumber
        ];
      }
      columnNumber *= 26;
      columnNumber += cellAddress.charCodeAt(i) - 64;
      i++;
    }
    invalidCellAddress(cellAddress);
  }
  function invalidCellAddress(cellAddress) {
    throw new Error('<c r="'.concat(cellAddress, '">'));
  }

  // node_modules/read-excel-file/modules/xlsx/parseSheet.js
  function _slicedToArray2(arr, i) {
    return _arrayWithHoles2(arr) || _iterableToArrayLimit2(arr, i) || _unsupportedIterableToArray3(arr, i) || _nonIterableRest2();
  }
  function _nonIterableRest2() {
    throw new TypeError("Invalid attempt to destructure non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _iterableToArrayLimit2(r, l) {
    var t = null == r ? null : "undefined" != typeof Symbol && r[Symbol.iterator] || r["@@iterator"];
    if (null != t) {
      var e, n, i, u, a = [], f = true, o = false;
      try {
        if (i = (t = t.call(r)).next, 0 === l) {
          if (Object(t) !== t) return;
          f = false;
        } else for (; !(f = (e = i.call(t)).done) && (a.push(e.value), a.length !== l); f = true) ;
      } catch (r2) {
        o = true, n = r2;
      } finally {
        try {
          if (!f && null != t["return"] && (u = t["return"](), Object(u) !== u)) return;
        } finally {
          if (o) throw n;
        }
      }
      return a;
    }
  }
  function _arrayWithHoles2(arr) {
    if (Array.isArray(arr)) return arr;
  }
  function _createForOfIteratorHelperLoose2(o, allowArrayLike) {
    var it = typeof Symbol !== "undefined" && o[Symbol.iterator] || o["@@iterator"];
    if (it) return (it = it.call(o)).next.bind(it);
    if (Array.isArray(o) || (it = _unsupportedIterableToArray3(o)) || allowArrayLike && o && typeof o.length === "number") {
      if (it) o = it;
      var i = 0;
      return function() {
        if (i >= o.length) return { done: true };
        return { done: false, value: o[i++] };
      };
    }
    throw new TypeError("Invalid attempt to iterate non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _unsupportedIterableToArray3(o, minLen) {
    if (!o) return;
    if (typeof o === "string") return _arrayLikeToArray3(o, minLen);
    var n = Object.prototype.toString.call(o).slice(8, -1);
    if (n === "Object" && o.constructor) n = o.constructor.name;
    if (n === "Map" || n === "Set") return Array.from(o);
    if (n === "Arguments" || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(n)) return _arrayLikeToArray3(o, minLen);
  }
  function _arrayLikeToArray3(arr, len) {
    if (len == null || len > arr.length) len = arr.length;
    for (var i = 0, arr2 = new Array(len); i < len; i++) arr2[i] = arr[i];
    return arr2;
  }
  var EMPTY_CELL_VALUE2 = null;
  function parseSheet(content, parseXml2, _ref) {
    var sharedStrings = _ref.sharedStrings, styles = _ref.styles, epoch1904 = _ref.epoch1904, dateFormatDetectionCache = _ref.dateFormatDetectionCache, options = _ref.options;
    var parseCellParameters = [
      sharedStrings,
      styles,
      epoch1904,
      dateFormatDetectionCache,
      options.dateFormat,
      // defaultDateFormat
      options.smartDateParser !== false,
      // dateTemplateParser
      options.parseNumber
      // parseNumberCustom
    ];
    var rows = [];
    var errors = [];
    var state = createInitialState();
    return parseXml2(content, state, onOpenTag, onCloseTag, onText, onProgress).then(function() {
      var _state$sheetData = state.sheetData, rowCount = _state$sheetData.rowCount, columnCount = _state$sheetData.columnCount, dataRowCount = _state$sheetData.dataRowCount, dataColumnCount = _state$sheetData.dataColumnCount;
      if (dataRowCount < rowCount) {
        rows = rows.slice(0, dataRowCount);
      }
      if (dataColumnCount < columnCount) {
        var i = 0;
        while (i < rows.length) {
          if (rows[i].length > dataColumnCount) {
            rows[i] = rows[i].slice(0, dataColumnCount);
          }
          i++;
        }
      }
      var startedAt = Date.now();
      for (var _iterator = _createForOfIteratorHelperLoose2(rows), _step; !(_step = _iterator()).done; ) {
        var row = _step.value;
        while (row.length < dataColumnCount) {
          row.push(EMPTY_CELL_VALUE2);
        }
      }
      return rows;
    });
    function createInitialState() {
      return {
        dimension: void 0,
        sheetData: void 0
      };
    }
    function getRowsFromState(state2) {
      return state2.sheetData.rows;
    }
    function setRowsInState(state2, rows2) {
      state2.sheetData.rowIndexShift += state2.sheetData.rows.length - rows2.length;
      state2.sheetData.rows = rows2;
    }
    function getErrorsFromState(state2) {
      return state2.sheetData.errors;
    }
    function setErrorsInState(state2, errors2) {
      state2.sheetData.errors = errors2;
    }
    var THROW_ON_FIRST_CELL_ERROR = true;
    function throwInvalidCellError(_ref2) {
      var row = _ref2.row, column2 = _ref2.column, error = _ref2.error;
      throw new InvalidSpreadsheetError("<c/> at row ".concat(row, ", col ").concat(column2, ": ").concat(error));
    }
    function onProgress(end) {
      var rowsRead = getRowsFromState(state);
      var errorsEncountered = getErrorsFromState(state);
      if (end) {
        rows = rows.concat(rowsRead);
        errors = errors.concat(errorsEncountered);
        if (errors.length > 0) {
          throwInvalidCellError(errors[0]);
        }
      } else {
        if (rowsRead.length > 1) {
          var finalizedRows = rowsRead.slice(0, -1);
          rows = rows.concat(finalizedRows);
          errors = errors.concat(errorsEncountered);
          setRowsInState(state, rowsRead.slice(-1));
          setErrorsInState(state, []);
        }
      }
    }
    function onOpenTag(tagName, attributes, state2) {
      if (tagName === "dimension") {
        state2.dimension = parseSheetDimensionRef(attributes.ref);
      } else if (tagName === "sheetData") {
        state2.sheetData = createInitialStateInSheetData();
      } else if (state2.sheetData) {
        onOpenTagInSheetData(tagName, attributes, state2.sheetData);
      }
    }
    function onCloseTag(tagName, state2) {
      if (state2.sheetData) {
        onCloseTagInSheetData(tagName, state2.sheetData);
      }
    }
    function onText(text2, state2) {
      if (state2.sheetData) {
        onTextInSheetData(text2, state2.sheetData);
      }
    }
    function parseSheetDimensionRef(ref) {
      var dimensions = ref.split(":").map(parseCellAddress);
      if (dimensions.length === 1) {
        dimensions = [dimensions[0], dimensions[0]];
      }
      return dimensions;
    }
    function createInitialStateInSheetData() {
      return {
        c: void 0,
        rows: [],
        row: void 0,
        rowNumber: void 0,
        // How many rows have been removed from the start of `state.rows`
        // as part of `onProgress()` handler calls.
        rowIndexShift: 0,
        // Current position in the sheet.
        cursor: [0, 0],
        // Total row count.
        rowCount: 0,
        // Total column count.
        columnCount: 0,
        // Non-empty row count.
        dataRowCount: 0,
        // Non-empty column count.
        dataColumnCount: 0,
        // Cell with errors.
        errors: []
      };
    }
    function onOpenTagInSheetData(tagName, attributes, state2) {
      if (tagName === "row") {
        if (attributes.r) {
          state2.rowNumber = Number(attributes.r);
        }
        state2.row = [];
      } else if (tagName === "c") {
        state2.c = createInitialStateInCell();
        state2.c.attributes = attributes;
      } else if (state2.c) {
        onOpenTagInCell(tagName, attributes, state2.c);
      }
    }
    function onCloseTagInSheetData(tagName, state2) {
      if (tagName === "row") {
        if (state2.rowNumber) {
          var previousRowNumber = state2.rowIndexShift + state2.rows.length;
          if (state2.rowNumber <= previousRowNumber) {
            throw new InvalidSpreadsheetError("Out-of-place <row/> number ".concat(state2.rowNumber, " follows <row/> number ").concat(previousRowNumber));
          }
          while (state2.rowNumber > state2.rowIndexShift + state2.rows.length + 1) {
            state2.rows.push([]);
          }
        }
        state2.rows.push(state2.row);
        if (state2.row.length > 0) {
          state2.dataRowCount = state2.rowNumber;
        }
        if (state2.rowNumber > state2.rowCount) {
          state2.rowCount = state2.rowNumber;
        }
        state2.row = void 0;
        state2.rowNumber = void 0;
      } else if (tagName === "c") {
        var cell = parseCellFromXmlData(state2.c);
        if (cell.row < state2.cursor[0] || cell.row === state2.cursor[0] && cell.column <= state2.cursor[1]) {
          throw new InvalidSpreadsheetError("Out-of-place <c/> at row ".concat(cell.row, " col ").concat(cell.column, " follows <c/> at row ").concat(state2.cursor[0], " col ").concat(state2.cursor[1]));
        }
        state2.cursor[0] = cell.row;
        state2.cursor[1] = cell.column;
        if (!state2.rowNumber) {
          state2.rowNumber = cell.row;
        }
        if (cell.error) {
          if (THROW_ON_FIRST_CELL_ERROR) {
            throwInvalidCellError(cell);
          }
          state2.errors.push(cell);
        } else if (cell.value !== EMPTY_CELL_VALUE2) {
          while (cell.column > state2.row.length + 1) {
            state2.row.push(EMPTY_CELL_VALUE2);
          }
          state2.row.push(cell.value);
          if (cell.column > state2.dataColumnCount) {
            state2.dataColumnCount = cell.column;
          }
        }
        if (cell.column > state2.columnCount) {
          state2.columnCount = cell.column;
        }
        state2.c = void 0;
      } else if (state2.c) {
        onCloseTagInCell(tagName, state2.c);
      }
    }
    function onTextInSheetData(text2, state2) {
      if (state2.c) {
        onTextInCell(text2, state2.c);
      }
    }
    function parseCellFromXmlData(_ref3) {
      var attributes = _ref3.attributes, inlineString = _ref3.inlineString, vText = _ref3.vText;
      var _parseCellAddress = parseCellAddress(attributes.r), _parseCellAddress2 = _slicedToArray2(_parseCellAddress, 2), row = _parseCellAddress2[0], column2 = _parseCellAddress2[1];
      var errorOrTypeAndValue = parseCellAndTrimValue(attributes.t, attributes.s, vText, inlineString, parseCellParameters, options.trim !== false);
      if (typeof errorOrTypeAndValue === "string") {
        return {
          row,
          column: column2,
          error: errorOrTypeAndValue
          // // Report the "raw" unparsed value of the cell for potential debugging.
          // // Also report the cell type and the format in case of a numeric value.
          // //
          // // For "inline string" cells, the value should actually be the `inlineString` argument
          // // rather than `vText` argument, but the only case when it could throw an error
          // // when parsing an "inline string" cell is `VALUE_MISSING` which means that
          // // `inlineString` argument is `undefined`, same as `vText` argument in this case,
          // // so the resulting `value` property is correct anyway.
          // //
          // value: vText,
          // type: attributes.t,
          // formatId: attributes.s
        };
      }
      return {
        row,
        column: column2,
        value: parseCellValue2(errorOrTypeAndValue[1], errorOrTypeAndValue[0])
      };
    }
    function parseCellAndTrimValue(t, s, v, inlineString, parameters, trimStrings) {
      var errorOrTypeAndValue = parseCellWithRepairAbility(t, s, v, inlineString, parameters);
      if (Array.isArray(errorOrTypeAndValue) && errorOrTypeAndValue[0] === "s") {
        if (trimStrings) {
          errorOrTypeAndValue[1] = errorOrTypeAndValue[1].trim();
        }
        if (errorOrTypeAndValue[1] === "") {
          return EMPTY_CELL;
        }
      }
      return errorOrTypeAndValue;
    }
    function parseCellWithRepairAbility(t, s, v, inlineString, parameters) {
      var errorOrTypeAndValue = parseCell(t, s, v, inlineString, parameters);
      if (errorOrTypeAndValue === "VALUE_MISSING") {
        switch (t || "n") {
          // * If the cell is defined by a formula.
          // * Or contains an inline string.
          // * Or contains a shared string.
          // * Or contains a boolean value.
          case "str":
          case "inlineStr":
          case "s":
          case "b":
            return EMPTY_CELL;
        }
      }
      if (t === "e") {
        return EMPTY_CELL;
      }
      return errorOrTypeAndValue;
    }
    function parseCellValue2(value, type) {
      if (type === "n") {
        if (options.parseNumber) {
          return options.parseNumber(value);
        }
        return value;
      } else if (type === "d") {
        return new Date(value);
      } else {
        return value;
      }
    }
    function createInitialStateInCell() {
      return {
        v: false,
        is: false,
        t: false,
        r: false,
        rPh: false,
        vText: void 0,
        inlineString: void 0,
        attributes: void 0
      };
    }
    function onOpenTagInCell(tagName, attributes, state2) {
      if (tagName === "v") {
        state2.v = true;
      } else if (tagName === "is") {
        state2.is = true;
        state2.inlineString = "";
      } else if (tagName === "t") {
        state2.t = true;
      } else if (tagName === "r") {
        state2.r = true;
      } else if (tagName === "rPh") {
        state2.rPh = true;
      }
    }
    function onCloseTagInCell(tagName, state2) {
      if (tagName === "v") {
        state2.v = false;
        state2.vText || (state2.vText = "");
      } else if (tagName === "is") {
        state2.is = false;
      } else if (tagName === "t") {
        state2.t = false;
      } else if (tagName === "r") {
        state2.r = false;
      } else if (tagName === "rPh") {
        state2.rPh = false;
      }
    }
    function onTextInCell(text2, state2) {
      if (state2.v) {
        state2.vText = text2;
      } else if (state2.is) {
        if (state2.rPh) {
        } else if (state2.t) {
          if (state2.r) {
            state2.inlineString += text2;
          } else {
            state2.inlineString = text2;
          }
        }
      }
    }
    function getSheetDimensions(cells) {
      var minRow = cells.length === 0 ? 0 : 1;
      var minCol = cells.length === 0 ? 0 : 1;
      var maxRow = 0;
      var maxCol = 0;
      for (var _iterator2 = _createForOfIteratorHelperLoose2(cells), _step2; !(_step2 = _iterator2()).done; ) {
        var cell = _step2.value;
        if (maxRow < cell.row) {
          maxRow = cell.row;
        }
        if (maxCol < cell.column) {
          maxCol = cell.column;
        }
      }
      return [[minRow, minCol], [maxRow, maxCol]];
    }
  }

  // node_modules/read-excel-file/modules/utility/convertValuesFromUint8ArraysToStrings.js
  function convertValuesFromUint8ArraysToStrings(entries) {
    checkpoint("convert files to strings");
    var convertedEntries = {};
    for (var _i = 0, _Object$keys = Object.keys(entries); _i < _Object$keys.length; _i++) {
      var key2 = _Object$keys[_i];
      convertedEntries[key2] = strFromU82(entries[key2]);
    }
    return convertedEntries;
  }
  function strFromU82(data) {
    if (typeof TextDecoder !== "undefined") {
      return new TextDecoder().decode(data);
    } else {
      return strFromU8(data);
    }
  }

  // node_modules/read-excel-file/modules/utility/isPromise.js
  function _typeof6(o) {
    "@babel/helpers - typeof";
    return _typeof6 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof6(o);
  }
  function isPromise(anything) {
    return _typeof6(anything) === "object" && typeof anything.then === "function";
  }

  // node_modules/read-excel-file/modules/xlsx/SheetNotFoundError.js
  function _typeof7(o) {
    "@babel/helpers - typeof";
    return _typeof7 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof7(o);
  }
  function _defineProperties4(target, props) {
    for (var i = 0; i < props.length; i++) {
      var descriptor = props[i];
      descriptor.enumerable = descriptor.enumerable || false;
      descriptor.configurable = true;
      if ("value" in descriptor) descriptor.writable = true;
      Object.defineProperty(target, _toPropertyKey5(descriptor.key), descriptor);
    }
  }
  function _createClass4(Constructor, protoProps, staticProps) {
    if (protoProps) _defineProperties4(Constructor.prototype, protoProps);
    if (staticProps) _defineProperties4(Constructor, staticProps);
    Object.defineProperty(Constructor, "prototype", { writable: false });
    return Constructor;
  }
  function _toPropertyKey5(arg) {
    var key2 = _toPrimitive5(arg, "string");
    return _typeof7(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive5(input, hint) {
    if (_typeof7(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof7(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _classCallCheck4(instance, Constructor) {
    if (!(instance instanceof Constructor)) {
      throw new TypeError("Cannot call a class as a function");
    }
  }
  function _inherits4(subClass, superClass) {
    if (typeof superClass !== "function" && superClass !== null) {
      throw new TypeError("Super expression must either be null or a function");
    }
    subClass.prototype = Object.create(superClass && superClass.prototype, { constructor: { value: subClass, writable: true, configurable: true } });
    Object.defineProperty(subClass, "prototype", { writable: false });
    if (superClass) _setPrototypeOf4(subClass, superClass);
  }
  function _createSuper4(Derived) {
    var hasNativeReflectConstruct = _isNativeReflectConstruct4();
    return function _createSuperInternal() {
      var Super = _getPrototypeOf4(Derived), result;
      if (hasNativeReflectConstruct) {
        var NewTarget = _getPrototypeOf4(this).constructor;
        result = Reflect.construct(Super, arguments, NewTarget);
      } else {
        result = Super.apply(this, arguments);
      }
      return _possibleConstructorReturn4(this, result);
    };
  }
  function _possibleConstructorReturn4(self, call) {
    if (call && (_typeof7(call) === "object" || typeof call === "function")) {
      return call;
    } else if (call !== void 0) {
      throw new TypeError("Derived constructors may only return object or undefined");
    }
    return _assertThisInitialized4(self);
  }
  function _assertThisInitialized4(self) {
    if (self === void 0) {
      throw new ReferenceError("this hasn't been initialised - super() hasn't been called");
    }
    return self;
  }
  function _wrapNativeSuper4(Class) {
    var _cache = typeof Map === "function" ? /* @__PURE__ */ new Map() : void 0;
    _wrapNativeSuper4 = function _wrapNativeSuper6(Class2) {
      if (Class2 === null || !_isNativeFunction4(Class2)) return Class2;
      if (typeof Class2 !== "function") {
        throw new TypeError("Super expression must either be null or a function");
      }
      if (typeof _cache !== "undefined") {
        if (_cache.has(Class2)) return _cache.get(Class2);
        _cache.set(Class2, Wrapper);
      }
      function Wrapper() {
        return _construct4(Class2, arguments, _getPrototypeOf4(this).constructor);
      }
      Wrapper.prototype = Object.create(Class2.prototype, { constructor: { value: Wrapper, enumerable: false, writable: true, configurable: true } });
      return _setPrototypeOf4(Wrapper, Class2);
    };
    return _wrapNativeSuper4(Class);
  }
  function _construct4(Parent, args, Class) {
    if (_isNativeReflectConstruct4()) {
      _construct4 = Reflect.construct.bind();
    } else {
      _construct4 = function _construct6(Parent2, args2, Class2) {
        var a = [null];
        a.push.apply(a, args2);
        var Constructor = Function.bind.apply(Parent2, a);
        var instance = new Constructor();
        if (Class2) _setPrototypeOf4(instance, Class2.prototype);
        return instance;
      };
    }
    return _construct4.apply(null, arguments);
  }
  function _isNativeReflectConstruct4() {
    if (typeof Reflect === "undefined" || !Reflect.construct) return false;
    if (Reflect.construct.sham) return false;
    if (typeof Proxy === "function") return true;
    try {
      Boolean.prototype.valueOf.call(Reflect.construct(Boolean, [], function() {
      }));
      return true;
    } catch (e) {
      return false;
    }
  }
  function _isNativeFunction4(fn) {
    return Function.toString.call(fn).indexOf("[native code]") !== -1;
  }
  function _setPrototypeOf4(o, p) {
    _setPrototypeOf4 = Object.setPrototypeOf ? Object.setPrototypeOf.bind() : function _setPrototypeOf6(o2, p2) {
      o2.__proto__ = p2;
      return o2;
    };
    return _setPrototypeOf4(o, p);
  }
  function _getPrototypeOf4(o) {
    _getPrototypeOf4 = Object.setPrototypeOf ? Object.getPrototypeOf.bind() : function _getPrototypeOf6(o2) {
      return o2.__proto__ || Object.getPrototypeOf(o2);
    };
    return _getPrototypeOf4(o);
  }
  var SheetNotFoundError = /* @__PURE__ */ function(_Error) {
    _inherits4(SheetNotFoundError2, _Error);
    var _super = _createSuper4(SheetNotFoundError2);
    function SheetNotFoundError2(sheet, sheets) {
      var _this;
      _classCallCheck4(this, SheetNotFoundError2);
      _this = _super.call(this, "Sheet not found: ".concat(typeof sheet === "number" ? sheet + ". Sheet count: " + sheets.length : sheet + ". Available sheets: " + sheets.join(", ")));
      _this.name = "SheetNotFoundError";
      _this.sheet = sheet;
      _this.sheets = sheets;
      return _this;
    }
    return _createClass4(SheetNotFoundError2);
  }(/* @__PURE__ */ _wrapNativeSuper4(Error));

  // node_modules/read-excel-file/modules/xlsx/parseSpreadsheetContents.js
  function _typeof8(o) {
    "@babel/helpers - typeof";
    return _typeof8 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof8(o);
  }
  function _createForOfIteratorHelperLoose3(o, allowArrayLike) {
    var it = typeof Symbol !== "undefined" && o[Symbol.iterator] || o["@@iterator"];
    if (it) return (it = it.call(o)).next.bind(it);
    if (Array.isArray(o) || (it = _unsupportedIterableToArray4(o)) || allowArrayLike && o && typeof o.length === "number") {
      if (it) o = it;
      var i = 0;
      return function() {
        if (i >= o.length) return { done: true };
        return { done: false, value: o[i++] };
      };
    }
    throw new TypeError("Invalid attempt to iterate non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _unsupportedIterableToArray4(o, minLen) {
    if (!o) return;
    if (typeof o === "string") return _arrayLikeToArray4(o, minLen);
    var n = Object.prototype.toString.call(o).slice(8, -1);
    if (n === "Object" && o.constructor) n = o.constructor.name;
    if (n === "Map" || n === "Set") return Array.from(o);
    if (n === "Arguments" || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(n)) return _arrayLikeToArray4(o, minLen);
  }
  function _arrayLikeToArray4(arr, len) {
    if (len == null || len > arr.length) len = arr.length;
    for (var i = 0, arr2 = new Array(len); i < len; i++) arr2[i] = arr[i];
    return arr2;
  }
  function ownKeys2(e, r) {
    var t = Object.keys(e);
    if (Object.getOwnPropertySymbols) {
      var o = Object.getOwnPropertySymbols(e);
      r && (o = o.filter(function(r2) {
        return Object.getOwnPropertyDescriptor(e, r2).enumerable;
      })), t.push.apply(t, o);
    }
    return t;
  }
  function _objectSpread2(e) {
    for (var r = 1; r < arguments.length; r++) {
      var t = null != arguments[r] ? arguments[r] : {};
      r % 2 ? ownKeys2(Object(t), true).forEach(function(r2) {
        _defineProperty2(e, r2, t[r2]);
      }) : Object.getOwnPropertyDescriptors ? Object.defineProperties(e, Object.getOwnPropertyDescriptors(t)) : ownKeys2(Object(t)).forEach(function(r2) {
        Object.defineProperty(e, r2, Object.getOwnPropertyDescriptor(t, r2));
      });
    }
    return e;
  }
  function _defineProperty2(obj, key2, value) {
    key2 = _toPropertyKey6(key2);
    if (key2 in obj) {
      Object.defineProperty(obj, key2, { value, enumerable: true, configurable: true, writable: true });
    } else {
      obj[key2] = value;
    }
    return obj;
  }
  function _toPropertyKey6(arg) {
    var key2 = _toPrimitive6(arg, "string");
    return _typeof8(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive6(input, hint) {
    if (_typeof8(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof8(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function parseSpreadsheetContents(parseXml2, contents_) {
    var options = arguments.length > 2 && arguments[2] !== void 0 ? arguments[2] : {};
    var contents = convertValuesFromUint8ArraysToStrings(contents_);
    checkpoint("parse spreadsheet info and file paths");
    return readFiles(getXmlFilesAtFixedPaths(), contents, parseXml2).then(function(_ref) {
      var spreadsheetInfo = _ref.spreadsheetInfo, filePaths = _ref.filePaths;
      checkpoint('parse "shared strings" and "styles"');
      return readFiles(getXmlFilesAtNonFixedPaths(filePaths), contents, parseXml2).then(function(_ref2) {
        var sharedStrings = _ref2.sharedStrings, styles = _ref2.styles;
        var sheetRelationIdsToRead = options.sheets ? options.sheets.map(function(sheet) {
          return getSheetRelationId(sheet, spreadsheetInfo.sheets);
        }) : spreadsheetInfo.sheets.map(function(_) {
          return _.relationId;
        });
        checkpoint("parse sheet".concat(sheetRelationIdsToRead.length === 1 ? "" : "s", " data"));
        var dateFormatDetectionCache = [];
        return readFiles(getSheetDataXmlFiles(filePaths, sheetRelationIdsToRead, {
          sharedStrings,
          styles,
          epoch1904: spreadsheetInfo.epoch1904,
          dateFormatDetectionCache,
          options
        }), contents, parseXml2).then(function(sheetsData) {
          checkpoint("end");
          return sheetRelationIdsToRead.map(function(sheetRelationId) {
            return {
              sheet: getSheetNameByRelationId(sheetRelationId, spreadsheetInfo.sheets),
              data: sheetsData[sheetRelationId]
            };
          });
        });
      });
    });
  }
  function parseSpreadsheetContentsInWorker(createWorkerFunction2, parseXml2, contents, options) {
    if (!(options && options.parseNumber)) {
      options = _objectSpread2(_objectSpread2({}, options), {}, {
        parseNumber: null
      });
    }
    return parseSpreadsheetContents(parseXml2, contents, options);
  }
  function getSheetRelationId(sheet, sheets) {
    if (typeof sheet === "string") {
      for (var _iterator = _createForOfIteratorHelperLoose3(sheets), _step; !(_step = _iterator()).done; ) {
        var _sheet = _step.value;
        if (_sheet.name === sheet) {
          return _sheet.relationId;
        }
      }
    } else {
      if (sheet <= sheets.length) {
        return sheets[sheet - 1].relationId;
      }
    }
    throw new SheetNotFoundError(sheet, sheets.map(function(_) {
      return _.name;
    }));
  }
  function getSheetNameByRelationId(sheetRelationId, sheets) {
    for (var _iterator2 = _createForOfIteratorHelperLoose3(sheets), _step2; !(_step2 = _iterator2()).done; ) {
      var sheet = _step2.value;
      if (sheet.relationId === sheetRelationId) {
        return sheet.name;
      }
    }
    throw new Error("Sheet relation ID not found: ".concat(sheetRelationId));
  }
  function getXmlFilesAtFixedPaths() {
    return {
      // Read the paths to certain files inside the `.xlsx` file, which is itself just a `.zip` archive.
      // These paths aren't standardized between different spreadsheet editors.
      // https://github.com/tidyverse/readxl/issues/104
      "xl/_rels/workbook.xml.rels": {
        name: "filePaths",
        parse: parseFilePaths
      },
      // General info on the spreadsheet.
      "xl/workbook.xml": {
        name: "spreadsheetInfo",
        parse: parseSpreadsheetInfo
      }
    };
  }
  function getXmlFilesAtNonFixedPaths(filePaths) {
    var _ref3;
    return _ref3 = {}, _defineProperty2(_ref3, filePaths.sharedStrings || "xl/sharedStrings.xml", {
      name: "sharedStrings",
      // `parseSharedStrings()` returns a `Promise`.
      parse: parseSharedStrings,
      // It seems that "sharedStrings.xml" is not required to exist.
      // For example, that could be the case when a spreadsheet doesn't contain any strings.
      // https://github.com/catamphetamine/read-excel-file/issues/85
      fallback: Promise.resolve([])
    }), _defineProperty2(_ref3, filePaths.styles || "xl/styles.xml", {
      name: "styles",
      parse: parseStyles,
      fallback: {}
    }), _ref3;
  }
  function getSheetDataXmlFiles(filePaths, sheetRelationIdsToRead, sheetDataParserParameters) {
    return Object.keys(filePaths.sheets).filter(function(sheetRelationId) {
      return sheetRelationIdsToRead.includes(sheetRelationId);
    }).reduce(function(filesInfo, sheetRelationId) {
      return _objectSpread2(_objectSpread2({}, filesInfo), {}, _defineProperty2({}, filePaths.sheets[sheetRelationId], {
        name: sheetRelationId,
        // `parseSheet()` returns a `Promise`.
        parse: function parse(content, parseXml2) {
          return parseSheet(content, parseXml2, sheetDataParserParameters);
        }
      }));
    }, {});
  }
  function readFiles(filesInfo, contents, parseXml2) {
    var results = {};
    var _loop = function _loop3() {
      var filePath = _Object$keys[_i];
      var fileInfo = filesInfo[filePath];
      results[fileInfo.name] = contents[filePath] === void 0 ? fileInfo.fallback === void 0 ? function() {
        throw new InvalidSpreadsheetError('"'.concat(filePath, '" file not found inside the `.xlsx` file'));
      }() : fileInfo.fallback : fileInfo.parse(contents[filePath], parseXml2);
    };
    for (var _i = 0, _Object$keys = Object.keys(filesInfo); _i < _Object$keys.length; _i++) {
      _loop();
    }
    var promises = [];
    var _loop2 = function _loop22() {
      var name = _Object$keys2[_i2];
      if (isPromise(results[name])) {
        promises.push(results[name].then(function(result) {
          results[name] = result;
        }));
      }
    };
    for (var _i2 = 0, _Object$keys2 = Object.keys(results); _i2 < _Object$keys2.length; _i2++) {
      _loop2();
    }
    if (promises.length > 0) {
      return Promise.all(promises).then(function() {
        return results;
      });
    }
    return results;
  }

  // node_modules/read-excel-file/modules/parseSheetData/InvalidError.js
  function _typeof9(o) {
    "@babel/helpers - typeof";
    return _typeof9 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof9(o);
  }
  function _defineProperties5(target, props) {
    for (var i = 0; i < props.length; i++) {
      var descriptor = props[i];
      descriptor.enumerable = descriptor.enumerable || false;
      descriptor.configurable = true;
      if ("value" in descriptor) descriptor.writable = true;
      Object.defineProperty(target, _toPropertyKey7(descriptor.key), descriptor);
    }
  }
  function _createClass5(Constructor, protoProps, staticProps) {
    if (protoProps) _defineProperties5(Constructor.prototype, protoProps);
    if (staticProps) _defineProperties5(Constructor, staticProps);
    Object.defineProperty(Constructor, "prototype", { writable: false });
    return Constructor;
  }
  function _toPropertyKey7(arg) {
    var key2 = _toPrimitive7(arg, "string");
    return _typeof9(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive7(input, hint) {
    if (_typeof9(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof9(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _classCallCheck5(instance, Constructor) {
    if (!(instance instanceof Constructor)) {
      throw new TypeError("Cannot call a class as a function");
    }
  }
  function _inherits5(subClass, superClass) {
    if (typeof superClass !== "function" && superClass !== null) {
      throw new TypeError("Super expression must either be null or a function");
    }
    subClass.prototype = Object.create(superClass && superClass.prototype, { constructor: { value: subClass, writable: true, configurable: true } });
    Object.defineProperty(subClass, "prototype", { writable: false });
    if (superClass) _setPrototypeOf5(subClass, superClass);
  }
  function _createSuper5(Derived) {
    var hasNativeReflectConstruct = _isNativeReflectConstruct5();
    return function _createSuperInternal() {
      var Super = _getPrototypeOf5(Derived), result;
      if (hasNativeReflectConstruct) {
        var NewTarget = _getPrototypeOf5(this).constructor;
        result = Reflect.construct(Super, arguments, NewTarget);
      } else {
        result = Super.apply(this, arguments);
      }
      return _possibleConstructorReturn5(this, result);
    };
  }
  function _possibleConstructorReturn5(self, call) {
    if (call && (_typeof9(call) === "object" || typeof call === "function")) {
      return call;
    } else if (call !== void 0) {
      throw new TypeError("Derived constructors may only return object or undefined");
    }
    return _assertThisInitialized5(self);
  }
  function _assertThisInitialized5(self) {
    if (self === void 0) {
      throw new ReferenceError("this hasn't been initialised - super() hasn't been called");
    }
    return self;
  }
  function _wrapNativeSuper5(Class) {
    var _cache = typeof Map === "function" ? /* @__PURE__ */ new Map() : void 0;
    _wrapNativeSuper5 = function _wrapNativeSuper6(Class2) {
      if (Class2 === null || !_isNativeFunction5(Class2)) return Class2;
      if (typeof Class2 !== "function") {
        throw new TypeError("Super expression must either be null or a function");
      }
      if (typeof _cache !== "undefined") {
        if (_cache.has(Class2)) return _cache.get(Class2);
        _cache.set(Class2, Wrapper);
      }
      function Wrapper() {
        return _construct5(Class2, arguments, _getPrototypeOf5(this).constructor);
      }
      Wrapper.prototype = Object.create(Class2.prototype, { constructor: { value: Wrapper, enumerable: false, writable: true, configurable: true } });
      return _setPrototypeOf5(Wrapper, Class2);
    };
    return _wrapNativeSuper5(Class);
  }
  function _construct5(Parent, args, Class) {
    if (_isNativeReflectConstruct5()) {
      _construct5 = Reflect.construct.bind();
    } else {
      _construct5 = function _construct6(Parent2, args2, Class2) {
        var a = [null];
        a.push.apply(a, args2);
        var Constructor = Function.bind.apply(Parent2, a);
        var instance = new Constructor();
        if (Class2) _setPrototypeOf5(instance, Class2.prototype);
        return instance;
      };
    }
    return _construct5.apply(null, arguments);
  }
  function _isNativeReflectConstruct5() {
    if (typeof Reflect === "undefined" || !Reflect.construct) return false;
    if (Reflect.construct.sham) return false;
    if (typeof Proxy === "function") return true;
    try {
      Boolean.prototype.valueOf.call(Reflect.construct(Boolean, [], function() {
      }));
      return true;
    } catch (e) {
      return false;
    }
  }
  function _isNativeFunction5(fn) {
    return Function.toString.call(fn).indexOf("[native code]") !== -1;
  }
  function _setPrototypeOf5(o, p) {
    _setPrototypeOf5 = Object.setPrototypeOf ? Object.setPrototypeOf.bind() : function _setPrototypeOf6(o2, p2) {
      o2.__proto__ = p2;
      return o2;
    };
    return _setPrototypeOf5(o, p);
  }
  function _getPrototypeOf5(o) {
    _getPrototypeOf5 = Object.setPrototypeOf ? Object.getPrototypeOf.bind() : function _getPrototypeOf6(o2) {
      return o2.__proto__ || Object.getPrototypeOf(o2);
    };
    return _getPrototypeOf5(o);
  }
  var InvalidError = /* @__PURE__ */ function(_Error) {
    _inherits5(InvalidError2, _Error);
    var _super = _createSuper5(InvalidError2);
    function InvalidError2(reason) {
      var _this;
      _classCallCheck5(this, InvalidError2);
      _this = _super.call(this, "invalid");
      _this.reason = reason;
      return _this;
    }
    return _createClass5(InvalidError2);
  }(/* @__PURE__ */ _wrapNativeSuper5(Error));

  // node_modules/read-excel-file/modules/parseSheetData/types/Number.js
  function NumberType(value) {
    if (typeof value === "string") {
      var stringifiedValue = value;
      value = Number(value);
      if (String(value) !== stringifiedValue) {
        throw new InvalidError("not_a_number");
      }
    }
    if (typeof value !== "number") {
      throw new InvalidError("not_a_number");
    }
    if (isNaN(value)) {
      throw new InvalidError("invalid_number");
    }
    if (!isFinite(value)) {
      throw new InvalidError("out_of_bounds");
    }
    return value;
  }

  // node_modules/read-excel-file/modules/parseSheetData/types/String.js
  function StringType(value) {
    if (typeof value === "string") {
      return value;
    }
    if (typeof value === "number") {
      if (isNaN(value)) {
        throw new InvalidError("invalid_number");
      }
      if (!isFinite(value)) {
        throw new InvalidError("out_of_bounds");
      }
      return String(value);
    }
    throw new InvalidError("not_a_string");
  }

  // node_modules/read-excel-file/modules/parseSheetData/types/Boolean.js
  function BooleanType(value) {
    if (typeof value === "boolean") {
      return value;
    }
    throw new InvalidError("not_a_boolean");
  }

  // node_modules/read-excel-file/modules/parseSheetData/types/Date.js
  function DateType(value) {
    if (value instanceof Date) {
      if (isNaN(value.valueOf())) {
        throw new InvalidError("out_of_bounds");
      }
      return value;
    }
    throw new InvalidError("not_a_date");
  }

  // node_modules/read-excel-file/modules/utility/isObject.js
  var objectConstructor = {}.constructor;
  function isObject(object) {
    return object !== void 0 && object !== null && object.constructor === objectConstructor;
  }

  // node_modules/read-excel-file/modules/parseSheetData/parseSheetData.js
  function _typeof10(o) {
    "@babel/helpers - typeof";
    return _typeof10 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof10(o);
  }
  function _slicedToArray3(arr, i) {
    return _arrayWithHoles3(arr) || _iterableToArrayLimit3(arr, i) || _unsupportedIterableToArray5(arr, i) || _nonIterableRest3();
  }
  function _iterableToArrayLimit3(r, l) {
    var t = null == r ? null : "undefined" != typeof Symbol && r[Symbol.iterator] || r["@@iterator"];
    if (null != t) {
      var e, n, i, u, a = [], f = true, o = false;
      try {
        if (i = (t = t.call(r)).next, 0 === l) {
          if (Object(t) !== t) return;
          f = false;
        } else for (; !(f = (e = i.call(t)).done) && (a.push(e.value), a.length !== l); f = true) ;
      } catch (r2) {
        o = true, n = r2;
      } finally {
        try {
          if (!f && null != t["return"] && (u = t["return"](), Object(u) !== u)) return;
        } finally {
          if (o) throw n;
        }
      }
      return a;
    }
  }
  function _toArray(arr) {
    return _arrayWithHoles3(arr) || _iterableToArray(arr) || _unsupportedIterableToArray5(arr) || _nonIterableRest3();
  }
  function _nonIterableRest3() {
    throw new TypeError("Invalid attempt to destructure non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _iterableToArray(iter) {
    if (typeof Symbol !== "undefined" && iter[Symbol.iterator] != null || iter["@@iterator"] != null) return Array.from(iter);
  }
  function _arrayWithHoles3(arr) {
    if (Array.isArray(arr)) return arr;
  }
  function ownKeys3(e, r) {
    var t = Object.keys(e);
    if (Object.getOwnPropertySymbols) {
      var o = Object.getOwnPropertySymbols(e);
      r && (o = o.filter(function(r2) {
        return Object.getOwnPropertyDescriptor(e, r2).enumerable;
      })), t.push.apply(t, o);
    }
    return t;
  }
  function _objectSpread3(e) {
    for (var r = 1; r < arguments.length; r++) {
      var t = null != arguments[r] ? arguments[r] : {};
      r % 2 ? ownKeys3(Object(t), true).forEach(function(r2) {
        _defineProperty3(e, r2, t[r2]);
      }) : Object.getOwnPropertyDescriptors ? Object.defineProperties(e, Object.getOwnPropertyDescriptors(t)) : ownKeys3(Object(t)).forEach(function(r2) {
        Object.defineProperty(e, r2, Object.getOwnPropertyDescriptor(t, r2));
      });
    }
    return e;
  }
  function _defineProperty3(obj, key2, value) {
    key2 = _toPropertyKey8(key2);
    if (key2 in obj) {
      Object.defineProperty(obj, key2, { value, enumerable: true, configurable: true, writable: true });
    } else {
      obj[key2] = value;
    }
    return obj;
  }
  function _toPropertyKey8(arg) {
    var key2 = _toPrimitive8(arg, "string");
    return _typeof10(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive8(input, hint) {
    if (_typeof10(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof10(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _createForOfIteratorHelperLoose4(o, allowArrayLike) {
    var it = typeof Symbol !== "undefined" && o[Symbol.iterator] || o["@@iterator"];
    if (it) return (it = it.call(o)).next.bind(it);
    if (Array.isArray(o) || (it = _unsupportedIterableToArray5(o)) || allowArrayLike && o && typeof o.length === "number") {
      if (it) o = it;
      var i = 0;
      return function() {
        if (i >= o.length) return { done: true };
        return { done: false, value: o[i++] };
      };
    }
    throw new TypeError("Invalid attempt to iterate non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
  }
  function _unsupportedIterableToArray5(o, minLen) {
    if (!o) return;
    if (typeof o === "string") return _arrayLikeToArray5(o, minLen);
    var n = Object.prototype.toString.call(o).slice(8, -1);
    if (n === "Object" && o.constructor) n = o.constructor.name;
    if (n === "Map" || n === "Set") return Array.from(o);
    if (n === "Arguments" || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(n)) return _arrayLikeToArray5(o, minLen);
  }
  function _arrayLikeToArray5(arr, len) {
    if (len == null || len > arr.length) len = arr.length;
    for (var i = 0, arr2 = new Array(len); i < len; i++) arr2[i] = arr[i];
    return arr2;
  }
  var EMPTY_CELL_VALUE3 = null;
  function parseSheetData(data, schema, optionsCustom) {
    checkpoint("parse sheet data using schema");
    var objects = [];
    var errors = [];
    var parsedRows = parseSheetDataWithPerRowErrors(data, schema, optionsCustom);
    var parsedRowIndex = 0;
    for (var _iterator = _createForOfIteratorHelperLoose4(parsedRows), _step; !(_step = _iterator()).done; ) {
      var _step$value = _step.value, object = _step$value.object, rowErrors = _step$value.errors;
      if (rowErrors) {
        errors = errors.concat(rowErrors.map(
          // Add row number property to each row error.
          function(rowError) {
            return _objectSpread3(_objectSpread3({}, rowError), {}, {
              row: parsedRowIndex + 1
            });
          }
        ));
      } else {
        objects.push(object);
      }
      parsedRowIndex++;
    }
    checkpoint("end");
    if (errors.length > 0) {
      return {
        errors
      };
    }
    return {
      objects
    };
  }
  function parseSheetDataWithPerRowErrors(data, schema, optionsCustom) {
    validateSchema(schema);
    var options = applyDefaultOptions(optionsCustom);
    var _data = _toArray(data), columns = _data[0], dataRows = _data.slice(1);
    return dataRows.map(function(row) {
      return parseDataRow(row, schema, columns, options);
    });
  }
  function parseDataRow(dataRow, schema, columns, options) {
    var schemaEntry = {
      schema
    };
    var _parseProperty = parseProperty(dataRow, schemaEntry, void 0, columns, options), value = _parseProperty.value, isEmptyValue2 = _parseProperty.isEmptyValue, errors = _parseProperty.errors, children = _parseProperty.children;
    var dummyParentObject = {
      // The "dummy" parent object has a "dummy" value.
      // This value is irrelevant because it won't be read anywhere.
      value: PARSED_OBJECT_TREE_START,
      // The "dummy" parent object is empty if the parsed row is empty.
      isEmptyValue: isEmptyValue2,
      // The "dummy" object has the same errors as the parsed row.
      errors,
      // The parsed object by default is not required to have any data
      // so the "dummy" object is not required.
      isRequired: void 0
    };
    var requiredErrors = runPendingRequiredValidations(
      schemaEntry,
      value,
      isEmptyValue2,
      errors,
      children,
      // Simulate a "dummy" parent object for the top-level object.
      dummyParentObject.isRequired,
      dummyParentObject.value,
      dummyParentObject.isEmptyValue,
      dummyParentObject.errors,
      columns
    );
    if (errors || requiredErrors) {
      return {
        errors: (errors || []).concat(requiredErrors || [])
      };
    }
    return {
      object: transformValue(value, isEmptyValue2, void 0, options)
    };
  }
  function parseObject(row, schema, path, columns, options) {
    var object = {};
    var isEmptyObject = true;
    var errors = [];
    var children = [];
    for (var _i = 0, _Object$keys = Object.keys(schema); _i < _Object$keys.length; _i++) {
      var key2 = _Object$keys[_i];
      var child = parseProperty(row, schema[key2], getPropertyPath(key2, path), columns, options);
      if (child.errors) {
        errors = errors.concat(child.errors);
      } else {
        object[key2] = transformValue(child.value, child.isEmptyValue, getPropertyPath(key2, path), options);
        if (isEmptyObject && !child.isEmptyValue) {
          isEmptyObject = false;
        }
      }
      children.push(_objectSpread3(_objectSpread3({}, child), {}, {
        // `schemaEntry` will be used when running `required` validation of this property (later),
        schemaEntry: schema[key2]
      }));
    }
    if (errors.length > 0) {
      return {
        // Return the errors.
        errors,
        // Return the `children` because `required` validations still have to be run (later).
        children
      };
    }
    return {
      value: object,
      isEmptyValue: isEmptyObject,
      // Return the `children` because `required` validations still have to be run (later).
      children
    };
  }
  function parseProperty(row, schemaEntry, path, columns, options) {
    var columnIndex = schemaEntry.column ? columns.indexOf(schemaEntry.column) : void 0;
    var isMissingColumn = schemaEntry.column ? columnIndex < 0 : void 0;
    var _ref = schemaEntry.column ? isMissingColumn ? {
      value: options.propertyValueWhenColumnIsMissing,
      isEmptyValue: true
    } : parseCellValueWithPossibleErrors(row[columnIndex], schemaEntry, columnIndex, options) : parseObject(row, schemaEntry.schema, path, columns, options), value = _ref.value, isEmptyValue2 = _ref.isEmptyValue, errors = _ref.errors, children = _ref.children;
    if (errors) {
      return {
        // Return the errors.
        errors,
        // Return the `children` because `required` validations still have to be run (later).
        children
      };
    }
    return {
      value,
      isEmptyValue: isEmptyValue2,
      // Return the `children` because `required` validations still have to be run (later).
      children
    };
  }
  function parseCellValueWithPossibleErrors(cellValue, schemaEntry, columnIndex, options) {
    var _parseCellValue = parseCellValue(cellValue, schemaEntry, options), value = _parseCellValue.value, isEmptyValue2 = _parseCellValue.isEmptyValue, errorMessage = _parseCellValue.error, errorReason = _parseCellValue.reason;
    if (errorMessage) {
      var error = createError({
        error: errorMessage,
        reason: errorReason,
        column: schemaEntry.column,
        columnIndex,
        valueType: schemaEntry.type,
        value: cellValue
      });
      return {
        errors: [error]
      };
    }
    return {
      value,
      isEmptyValue: isEmptyValue2
    };
  }
  function parseCellValue(cellValue, schemaEntry, options) {
    if (cellValue === void 0) {
      return {
        value: options.propertyValueWhenColumnIsMissing,
        isEmptyValue: true
      };
    }
    if (cellValue === EMPTY_CELL_VALUE3) {
      return {
        value: options.propertyValueWhenCellIsEmpty,
        isEmptyValue: true
      };
    }
    if (Array.isArray(schemaEntry.type)) {
      return parseArrayValue(cellValue, schemaEntry, options);
    }
    return parseValue(cellValue, schemaEntry, options);
  }
  function parseArrayValue(value, schemaEntry, options) {
    if (typeof value !== "string") {
      return {
        error: "not_a_string"
      };
    }
    var isEmptyArray = true;
    var errors = [];
    var reasons = [];
    var values = parseSeparatedSubstrings(value, options.separatorCharacter).map(function(substring) {
      if (errors.length > 0) {
        return;
      }
      if (!substring) {
        errors.push("invalid");
        reasons.push("syntax");
        return;
      }
      var _parseValue = parseValue(substring, schemaEntry, options), value2 = _parseValue.value, isEmptyValue2 = _parseValue.isEmptyValue, error = _parseValue.error, reason = _parseValue.reason;
      if (error) {
        errors.push(error);
        reasons.push(reason);
        return;
      }
      if (isEmptyArray && !isEmptyValue2) {
        isEmptyArray = false;
      }
      return value2;
    });
    if (errors.length > 0) {
      return {
        error: errors[0],
        reason: reasons[0]
      };
    }
    return {
      value: values,
      isEmptyValue: isEmptyArray
    };
  }
  function parseValue(value, schemaEntry, options) {
    if (value === EMPTY_CELL_VALUE3) {
      return {
        value,
        isEmptyValue: true
      };
    }
    var result;
    if (schemaEntry.type) {
      result = parseValueOfType(
        value,
        // Get the type of the value.
        //
        // Handle the case if it's a comma-separated value.
        // Example `type`: String[]
        // Example Input Value: 'Barack Obama, "String, with, colons", Donald Trump'
        // Example Parsed Value: ['Barack Obama', 'String, with, colons', 'Donald Trump']
        //
        Array.isArray(schemaEntry.type) ? schemaEntry.type[0] : schemaEntry.type,
        options
      );
    } else {
      result = {
        value
      };
    }
    if (result.error) {
      return result;
    }
    if (value === EMPTY_CELL_VALUE3) {
      return {
        value,
        isEmptyValue: true
      };
    }
    if (schemaEntry.oneOf) {
      var errorAndReason = validateOneOf(result.value, schemaEntry.oneOf);
      if (errorAndReason) {
        return errorAndReason;
      }
    }
    if (schemaEntry.validate) {
      try {
        schemaEntry.validate(result.value);
      } catch (error) {
        return {
          error: error.message
        };
      }
    }
    return {
      value: result.value,
      isEmptyValue: isEmptyValue(result.value)
    };
  }
  function validateOneOf(value, oneOf) {
    if (oneOf.indexOf(value) < 0) {
      return {
        error: "invalid",
        reason: "unknown"
      };
    }
  }
  function parseValueOfType(value, type) {
    switch (type) {
      case String:
        return parseValueUsingTypeParser(value, StringType);
      case Number:
        return parseValueUsingTypeParser(value, NumberType);
      case Date:
        return parseValueUsingTypeParser(value, DateType);
      case Boolean:
        return parseValueUsingTypeParser(value, BooleanType);
      default:
        if (typeof type !== "function") {
          throw new Error("Unsupported schema `type`: ".concat(type && type.name || type));
        }
        return parseValueUsingTypeParser(value, type);
    }
  }
  function parseValueUsingTypeParser(value, type) {
    try {
      var parsedValue = type(value);
      if (parsedValue === void 0) {
        return {
          value: EMPTY_CELL_VALUE3
        };
      }
      return {
        value: parsedValue
      };
    } catch (error) {
      var result = {
        error: error.message
      };
      if (error.reason) {
        result.reason = error.reason;
      }
      return result;
    }
  }
  function getNextSubstring(string, separatorCharacter, startIndex) {
    var i = 0;
    var substring = "";
    while (startIndex + i < string.length) {
      var character = string[startIndex + i];
      if (character === separatorCharacter) {
        return [substring, i];
      } else {
        substring += character;
        i++;
      }
    }
    return [substring, i];
  }
  function parseSeparatedSubstrings(string, separatorCharacter) {
    var elements = [];
    var index = 0;
    while (index < string.length) {
      var _getNextSubstring = getNextSubstring(string, separatorCharacter, index), _getNextSubstring2 = _slicedToArray3(_getNextSubstring, 2), substring = _getNextSubstring2[0], length = _getNextSubstring2[1];
      index += length + separatorCharacter.length;
      elements.push(substring.trim());
    }
    return elements;
  }
  function transformValue(value, isEmptyValue2, path, options) {
    if (isEmptyValue2) {
      if (isObject(value)) {
        return options.transformEmptyObject(value, {
          path
        });
      } else if (Array.isArray(value)) {
        return options.transformEmptyArray(value, {
          path
        });
      }
    }
    return value;
  }
  function getPropertyPath(propertyName, parentObjectPath) {
    return "".concat(parentObjectPath ? parentObjectPath + "." : "").concat(propertyName);
  }
  function runPendingRequiredValidations(schemaEntry, value, isEmptyValue2, errors, children, parentObjectIsRequired, parentObjectValue, parentObjectValueIsEmpty, parentObjectErrors, columns) {
    var requiredErrors = [];
    var isRequired = isPropertyRequired(schemaEntry, parentObjectIsRequired, parentObjectValue, parentObjectValueIsEmpty, parentObjectErrors);
    if (isRequired && isEmptyValue2) {
      requiredErrors.push(createError({
        error: "required",
        column: schemaEntry.column,
        columnIndex: columns.indexOf(schemaEntry.column),
        valueType: schemaEntry.type,
        value
      }));
    }
    if (children) {
      for (var _iterator2 = _createForOfIteratorHelperLoose4(children), _step2; !(_step2 = _iterator2()).done; ) {
        var child = _step2.value;
        var requiredErrorsOfChild = runPendingRequiredValidations(
          child.schemaEntry,
          child.value,
          child.isEmptyValue,
          child.errors,
          child.children,
          // The following properties describe the parent object of the `child`,
          // i.e. the current (iterated) object.
          isRequired,
          value,
          isEmptyValue2,
          errors,
          columns
        );
        if (requiredErrorsOfChild) {
          requiredErrors = requiredErrors.concat(requiredErrorsOfChild);
        }
      }
    }
    if (requiredErrors.length > 0) {
      return requiredErrors;
    }
  }
  function isPropertyRequired(schemaEntry, parentObjectIsRequired, parentObjectValue, parentObjectValueIsEmpty, parentObjectErrors) {
    if (parentObjectIsRequired === false && (parentObjectValueIsEmpty || parentObjectErrors)) {
      return false;
    }
    return schemaEntry.required && (typeof schemaEntry.required === "boolean" ? schemaEntry.required : (
      // If there were any non-`required` errors when parsing the parent object,
      // the `parentObject` will be `undefined`. In that case, "complex" `required()`
      // validations — the ones where `required` is a function — can't really be run
      // because those validations assume a fully and correctly parsed parent object
      // be passed as an argument, and the thing is that the `parentObject` is unknown.
      // As a result, only "basic" `required` validations could be run,
      // i.e. the ones where `required` is just a boolean, and "complex" `required`
      // validations, i.e. the ones where `required` is a functions, should be skipped,
      // because it's better to skip some `required` errors than to trigger falsy ones.
      parentObjectErrors ? false : schemaEntry.required(parentObjectValue)
    ));
  }
  function createError(_ref2) {
    var column2 = _ref2.column, columnIndex = _ref2.columnIndex, valueType = _ref2.valueType, value = _ref2.value, errorMessage = _ref2.error, reason = _ref2.reason;
    var error = {
      error: errorMessage,
      column: column2,
      columnIndex,
      value
    };
    if (reason) {
      error.reason = reason;
    }
    if (valueType) {
      error.type = valueType;
    }
    return error;
  }
  function validateSchema(schema) {
    for (var _i2 = 0, _Object$keys2 = Object.keys(schema); _i2 < _Object$keys2.length; _i2++) {
      var key2 = _Object$keys2[_i2];
      var schemaEntry = schema[key2];
      if (_typeof10(schemaEntry.type) === "object" && !Array.isArray(schemaEntry.type)) {
        throw new Error("When defining a nested schema, use a `schema` property instead of a `type` property");
      }
      if (!schemaEntry.schema) {
        if (!schemaEntry.column) {
          throw new Error('"column" not defined for schema entry "'.concat(key2, '".'));
        }
      }
    }
    validateObjectSchemaRequiredProperty(schema, void 0);
  }
  function validateObjectSchemaRequiredProperty(schema, required) {
    if (required !== void 0 && required !== false) {
      throw new Error("In a schema, a nested object can have a `required` property but the only allowed value is `undefined` or `false`. Otherwise, a \"required\" error for a nested object would have to include a specific `column` title and a nested object doesn't have one. You've specified the following `required`: ".concat(required));
    }
    for (var _i3 = 0, _Object$keys3 = Object.keys(schema); _i3 < _Object$keys3.length; _i3++) {
      var key2 = _Object$keys3[_i3];
      if (isObject(schema[key2].schema)) {
        if (schema[key2].column) {
          throw new Error("In a schema, `column` property is only allowed when describing a property value rather than a nested object. Key: ".concat(key2, ". Schema:\n").concat(JSON.stringify(schema[key2], null, 2)));
        }
        validateObjectSchemaRequiredProperty(schema[key2].schema, schema[key2].required);
      }
    }
  }
  function isEmptyValue(value) {
    return value === void 0 || value === null;
  }
  var DEFAULT_OPTIONS = {
    propertyValueWhenColumnIsMissing: void 0,
    propertyValueWhenCellIsEmpty: null,
    // shouldSkipRequiredValidationWhenColumnIsMissing: () => false,
    // `transformEmptyObject(object, { path })` applies to both the top-level object
    // and any of its nested objects.
    transformEmptyObject: function transformEmptyObject() {
      return null;
    },
    transformEmptyArray: function transformEmptyArray() {
      return null;
    },
    separatorCharacter: ","
  };
  function applyDefaultOptions(options) {
    if (options) {
      return _objectSpread3(_objectSpread3({}, DEFAULT_OPTIONS), options);
    } else {
      return DEFAULT_OPTIONS;
    }
  }
  var PARSED_OBJECT_TREE_START = {};

  // node_modules/read-excel-file/modules/export/parseSheet.js
  function _typeof11(o) {
    "@babel/helpers - typeof";
    return _typeof11 = "function" == typeof Symbol && "symbol" == typeof Symbol.iterator ? function(o2) {
      return typeof o2;
    } : function(o2) {
      return o2 && "function" == typeof Symbol && o2.constructor === Symbol && o2 !== Symbol.prototype ? "symbol" : typeof o2;
    }, _typeof11(o);
  }
  var _excluded2 = ["schema"];
  function ownKeys4(e, r) {
    var t = Object.keys(e);
    if (Object.getOwnPropertySymbols) {
      var o = Object.getOwnPropertySymbols(e);
      r && (o = o.filter(function(r2) {
        return Object.getOwnPropertyDescriptor(e, r2).enumerable;
      })), t.push.apply(t, o);
    }
    return t;
  }
  function _objectSpread4(e) {
    for (var r = 1; r < arguments.length; r++) {
      var t = null != arguments[r] ? arguments[r] : {};
      r % 2 ? ownKeys4(Object(t), true).forEach(function(r2) {
        _defineProperty4(e, r2, t[r2]);
      }) : Object.getOwnPropertyDescriptors ? Object.defineProperties(e, Object.getOwnPropertyDescriptors(t)) : ownKeys4(Object(t)).forEach(function(r2) {
        Object.defineProperty(e, r2, Object.getOwnPropertyDescriptor(t, r2));
      });
    }
    return e;
  }
  function _defineProperty4(obj, key2, value) {
    key2 = _toPropertyKey9(key2);
    if (key2 in obj) {
      Object.defineProperty(obj, key2, { value, enumerable: true, configurable: true, writable: true });
    } else {
      obj[key2] = value;
    }
    return obj;
  }
  function _toPropertyKey9(arg) {
    var key2 = _toPrimitive9(arg, "string");
    return _typeof11(key2) === "symbol" ? key2 : String(key2);
  }
  function _toPrimitive9(input, hint) {
    if (_typeof11(input) !== "object" || input === null) return input;
    var prim = input[Symbol.toPrimitive];
    if (prim !== void 0) {
      var res = prim.call(input, hint || "default");
      if (_typeof11(res) !== "object") return res;
      throw new TypeError("@@toPrimitive must return a primitive value.");
    }
    return (hint === "string" ? String : Number)(input);
  }
  function _objectWithoutProperties2(source, excluded) {
    if (source == null) return {};
    var target = _objectWithoutPropertiesLoose2(source, excluded);
    var key2, i;
    if (Object.getOwnPropertySymbols) {
      var sourceSymbolKeys = Object.getOwnPropertySymbols(source);
      for (i = 0; i < sourceSymbolKeys.length; i++) {
        key2 = sourceSymbolKeys[i];
        if (excluded.indexOf(key2) >= 0) continue;
        if (!Object.prototype.propertyIsEnumerable.call(source, key2)) continue;
        target[key2] = source[key2];
      }
    }
    return target;
  }
  function _objectWithoutPropertiesLoose2(source, excluded) {
    if (source == null) return {};
    var target = {};
    var sourceKeys = Object.keys(source);
    var key2, i;
    for (i = 0; i < sourceKeys.length; i++) {
      key2 = sourceKeys[i];
      if (excluded.indexOf(key2) >= 0) continue;
      target[key2] = source[key2];
    }
    return target;
  }
  function parseSheet2(createWorkerFunction2, parseXml2, contents, sheet, optionsWithSchema) {
    var _ref = optionsWithSchema || {}, schema = _ref.schema, options = _objectWithoutProperties2(_ref, _excluded2);
    return parseSpreadsheetContentsInWorker(createWorkerFunction2, parseXml2, contents, _objectSpread4(_objectSpread4({}, options), {}, {
      sheets: [sheet === void 0 ? 1 : sheet]
    })).then(function(sheets) {
      var sheetData = sheets[0].data;
      if (schema) {
        return parseSheetData(sheetData, schema);
      }
      return sheetData;
    });
  }

  // node_modules/read-excel-file/modules/export/readSheetBrowser.js
  function readSheet(input, sheet, options) {
    if (!options && sheet && typeof sheet !== "number" && typeof sheet !== "string") {
      options = sheet;
      sheet = void 0;
    }
    return unpackXlsxFile(input).then(function(contents) {
      return parseSheet2(createWorkerFunctionInBrowser, parseXml, contents, sheet, options);
    });
  }

  // internal/webshell/static_src/admin_console/owner_migration_file.ts
  var import_xmldom = __toESM(require_lib(), 1);
  var MAX_FILE_SIZE = 1024 * 1024;
  var MAX_ZIP_ENTRIES = 128;
  var MAX_ZIP_ENTRY_SIZE = 4 * MAX_FILE_SIZE;
  var MAX_ZIP_TOTAL_SIZE = 16 * MAX_FILE_SIZE;
  var XML_DECODER = new TextDecoder();
  var UTF8_DECODER = new TextDecoder("utf-8", { fatal: true });
  var ZIP_MAGIC = [80, 75];
  var BIFF_MAGIC = [208, 207, 17, 224, 161, 177, 26, 225];
  var SPREADSHEET_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main";
  var OFFICE_RELATIONSHIP_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships";
  var PACKAGE_RELATIONSHIP_NS = "http://schemas.openxmlformats.org/package/2006/relationships";
  var nonEmpty = (value) => value !== null && value !== void 0 && String(value).trim() !== "";
  var blankRow = (row) => row.every((value) => !nonEmpty(value));
  var parseXML = (xml) => {
    const document2 = new import_xmldom.DOMParser({ locator: false, onError: () => {
      throw new Error("invalid XML");
    } }).parseFromString(xml, "application/xml");
    if (!document2.documentElement) throw new Error("invalid XML");
    return document2;
  };
  var firstElement = (parent, localName, namespaceURI) => {
    for (let child = parent.firstChild; child; child = child.nextSibling) {
      if (child.nodeType === 1 && (child.localName || child.nodeName) === localName && (namespaceURI === void 0 || child.namespaceURI === namespaceURI)) return child;
    }
    return void 0;
  };
  var hasFormula = (element) => {
    if ((element.localName || element.nodeName) === "f") return true;
    for (let child = element.firstChild; child; child = child.nextSibling) if (child.nodeType === 1 && hasFormula(child)) return true;
    return false;
  };
  var resolveZipPath = (target) => {
    const parts = [];
    const path = target.startsWith("/") ? target.slice(1) : `xl/${target}`;
    for (const part of path.split("/")) {
      if (!part || part === ".") continue;
      if (part === "..") {
        if (!parts.length) throw new Error("invalid worksheet relationship");
        parts.pop();
      } else parts.push(part);
    }
    const resolved = parts.join("/");
    if (!resolved.startsWith("xl/")) throw new Error("invalid worksheet relationship");
    return resolved;
  };
  var firstWorksheetPath = (archive) => {
    const workbookRoot = parseXML(XML_DECODER.decode(archive["xl/workbook.xml"])).documentElement;
    if (!workbookRoot || workbookRoot.localName !== "workbook" || workbookRoot.namespaceURI !== SPREADSHEET_NS) throw new Error("missing workbook");
    const sheets = firstElement(workbookRoot, "sheets", SPREADSHEET_NS);
    const firstSheet = sheets ? firstElement(sheets, "sheet", SPREADSHEET_NS) : void 0;
    const relationshipID = firstSheet?.getAttributeNS(OFFICE_RELATIONSHIP_NS, "id") || firstSheet?.getAttribute("r:id");
    if (!relationshipID) throw new Error("missing first worksheet relationship");
    const relationshipsRoot = parseXML(XML_DECODER.decode(archive["xl/_rels/workbook.xml.rels"])).documentElement;
    if (!relationshipsRoot || relationshipsRoot.localName !== "Relationships" || relationshipsRoot.namespaceURI !== PACKAGE_RELATIONSHIP_NS) throw new Error("missing worksheet relationships");
    const relationship = [...relationshipsRoot.childNodes].find((child) => child.nodeType === 1 && child.localName === "Relationship" && child.namespaceURI === PACKAGE_RELATIONSHIP_NS && child.getAttribute("Id") === relationshipID);
    const target = relationship?.getAttribute("Target");
    if (!target) throw new Error("missing first worksheet");
    return resolveZipPath(target);
  };
  var rejectFormulas = async (file) => {
    let entries = 0;
    let total = 0;
    const archive = unzipSync(new Uint8Array(await file.arrayBuffer()), { filter: ({ name, originalSize }) => {
      entries += 1;
      if (entries > MAX_ZIP_ENTRIES || !Number.isSafeInteger(originalSize) || originalSize < 0 || originalSize > MAX_ZIP_ENTRY_SIZE || total > MAX_ZIP_TOTAL_SIZE - originalSize) throw new Error("Excel ZIP \u89E3\u538B\u5927\u5C0F\u8D85\u51FA\u9650\u5236");
      total += originalSize;
      return name.endsWith(".xml") || name.endsWith(".xml.rels");
    } });
    const worksheet = archive[firstWorksheetPath(archive)];
    if (!worksheet || hasFormula(parseXML(XML_DECODER.decode(worksheet)).documentElement)) throw new Error("Excel \u6587\u4EF6\u4E0D\u80FD\u5305\u542B\u516C\u5F0F");
  };
  var csvRows = (source) => {
    const rows = [];
    let row = [];
    let cell = "";
    let quoted = false;
    const data = source.replace(/^\uFEFF/, "");
    for (let index = 0; index < data.length; index += 1) {
      const char = data[index];
      if (quoted) {
        if (char === '"' && data[index + 1] === '"') {
          cell += '"';
          index += 1;
          continue;
        }
        if (char === '"') {
          quoted = false;
          continue;
        }
        cell += char;
        continue;
      }
      if (char === '"') {
        if (cell) throw new Error("CSV \u5F15\u53F7\u4F4D\u7F6E\u65E0\u6548");
        quoted = true;
      } else if (char === ",") {
        row.push(cell.trim());
        cell = "";
      } else if (char === "\r" || char === "\n") {
        if (char === "\r" && data[index + 1] === "\n") index += 1;
        row.push(cell.trim());
        cell = "";
        if (!blankRow(row)) rows.push(row);
        row = [];
      } else cell += char;
    }
    if (quoted) throw new Error("CSV \u5F15\u53F7\u672A\u95ED\u5408");
    if (cell || row.length) {
      row.push(cell.trim());
      if (!blankRow(row)) rows.push(row);
    }
    return rows;
  };
  var startsWith = (value, magic) => magic.every((byte, index) => value[index] === byte);
  var decodedCSV = async (file) => {
    try {
      return csvRows(UTF8_DECODER.decode(await file.arrayBuffer()));
    } catch (error) {
      if (error instanceof TypeError) throw new Error("CSV \u6587\u4EF6\u5FC5\u987B\u662F UTF-8 \u7F16\u7801");
      throw error;
    }
  };
  async function ownerMigrationRowsFromFile(file) {
    const filename = file.name.toLowerCase();
    if (file.size > MAX_FILE_SIZE) throw new Error("\u4E0A\u4F20\u6587\u4EF6\u4E0D\u80FD\u8D85\u8FC7 1 MiB");
    if (!filename.endsWith(".xlsx") && !filename.endsWith(".xls") && !filename.endsWith(".csv")) throw new Error("\u4EC5\u652F\u6301 CSV\u3001XLSX \u6216\u65E7\u6269\u5C55\u540D XLS \u6587\u4EF6");
    const prefix = new Uint8Array(await file.slice(0, 8).arrayBuffer());
    const xlsx = filename.endsWith(".xlsx") || filename.endsWith(".xls") && startsWith(prefix, ZIP_MAGIC);
    if (!xlsx) {
      if (filename.endsWith(".xls") && startsWith(prefix, BIFF_MAGIC)) throw new Error("\u4E0D\u652F\u6301\u4E8C\u8FDB\u5236 BIFF .xls\uFF0C\u8BF7\u53E6\u5B58\u4E3A CSV \u6216 XLSX");
      return decodedCSV(file);
    }
    let rows;
    try {
      await rejectFormulas(file);
      rows = await readSheet(file, { trim: false });
    } catch (error) {
      if (error instanceof Error && error.message === "Excel \u6587\u4EF6\u4E0D\u80FD\u5305\u542B\u516C\u5F0F") throw error;
      throw new Error("Excel \u6587\u4EF6\u65E0\u6CD5\u89E3\u6790");
    }
    if (!rows.length) throw new Error("Excel \u7B2C\u4E00\u5F20\u5DE5\u4F5C\u8868\u4E0D\u80FD\u4E3A\u7A7A");
    return rows.filter((row) => !blankRow(row)).map((row) => row.map((value) => value === null || value === void 0 ? "" : String(value).trim()));
  }
  var xmlText = (value) => value.replace(/[&<>'"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&apos;", '"': "&quot;" })[char] || char);
  var column = (index) => {
    let value = index + 1;
    let out = "";
    while (value) {
      const remainder = (value - 1) % 26;
      out = String.fromCharCode(65 + remainder) + out;
      value = Math.floor((value - 1) / 26);
    }
    return out;
  };
  function ownerMigrationWorkbookXLSX(headers, rows) {
    const cells = (values, row) => values.map((value, index) => `<c r="${column(index)}${row}" t="inlineStr"><is><t>${xmlText(value)}</t></is></c>`).join("");
    const sheetRows = [headers, ...rows].map((values, index) => `<row r="${index + 1}">${cells(values, index + 1)}</row>`).join("");
    const sheet = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>${sheetRows}</sheetData></worksheet>`;
    return zipSync({
      "[Content_Types].xml": strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>'),
      "_rels/.rels": strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>'),
      "xl/workbook.xml": strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="owner_migration" sheetId="1" r:id="rId1"/></sheets></workbook>'),
      "xl/_rels/workbook.xml.rels": strToU8('<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>'),
      "xl/worksheets/sheet1.xml": strToU8(sheet)
    });
  }
  function ownerMigrationTemplateXLSX() {
    return ownerMigrationWorkbookXLSX(["external_userid", "\u662F\u5426\u8FC1\u79FB", "\u5F53\u524D\u8D1F\u8D23\u4EBAuserid", "\u5BA2\u6237\u5907\u6CE8\u540D", "\u5907\u6CE8"], []);
  }

  // internal/webshell/static_src/admin_console/owner_handoff_host.ts
  var donorURL = "/static/admin_console/owner_migration_dd8d60d.html";
  var key = () => `owner-handoff-${crypto.getRandomValues(new Uint32Array(2)).join("-")}`;
  var text = (value) => String(value ?? "").trim();
  var esc = (value) => text(value).replace(/[&<>'"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[char] || char);
  var requestFailure = (message, status) => {
    const error = new Error(message);
    error.httpStatus = status;
    error.userMessage = true;
    return error;
  };
  async function api(path, init) {
    const headers = new Headers(init?.headers);
    if ((init?.method || "GET").toUpperCase() !== "GET") {
      if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
      if (!headers.has("X-CSRF-Token")) {
        const cookie = document.cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith("aicrm_admin_csrf="));
        if (cookie) headers.set("X-CSRF-Token", decodeURIComponent(cookie.slice("aicrm_admin_csrf=".length)));
      }
    }
    const response = await fetch(path, { credentials: "same-origin", ...init, headers });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw requestFailure(ownerHandoffRequestMessage(response.status, text(body.error)), response.status);
    return body;
  }
  var localOwnerHandoffMessages = /* @__PURE__ */ new Set([
    "\u8BF7\u5148\u9009\u62E9\u4E0D\u540C\u7684\u539F\u8D1F\u8D23\u4EBA\u548C\u76EE\u6807\u8D1F\u8D23\u4EBA",
    "\u8BF7\u9009\u62E9\u5305\u542B\u65E7\u6A21\u677F\u4E94\u5217\u7684 XLSX\u3001XLS \u6216 CSV \u6587\u4EF6",
    "\u6BCF\u4E00\u884C\u5FC5\u987B\u5305\u542B\u65E7\u6A21\u677F\u7684\u4E94\u5217",
    "\u8BF7\u5148\u4E0A\u4F20\u65E7\u6A21\u677F\u540D\u5355",
    "\u8BF7\u5148\u751F\u6210\u9884\u89C8",
    "\u786E\u8BA4\u77ED\u8BED\u4E0D\u5339\u914D"
  ]);
  function ownerHandoffRequestMessage(status, detail) {
    const mapped = {
      owner_handoff_provider_unavailable: "\u8FC1\u79FB\u670D\u52A1\u6682\u4E0D\u53EF\u7528\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
      owner_handoff_conflict: "\u8FC1\u79FB\u72B6\u6001\u5DF2\u53D8\u5316\uFF0C\u8BF7\u91CD\u65B0\u751F\u6210\u9884\u89C8\u540E\u91CD\u8BD5\u3002",
      invalid_request: "\u8FC1\u79FB\u8BF7\u6C42\u65E0\u6548\uFF0C\u8BF7\u68C0\u67E5\u586B\u5199\u5185\u5BB9\u540E\u91CD\u8BD5\u3002"
    }[detail];
    if (mapped) return mapped;
    if (status === 401) return "\u767B\u5F55\u72B6\u6001\u5DF2\u5931\u6548\uFF0C\u8BF7\u91CD\u65B0\u767B\u5F55\u540E\u7EE7\u7EED\u3002";
    if (status === 403) return "\u6CA1\u6709\u8D1F\u8D23\u4EBA\u8FC1\u79FB\u64CD\u4F5C\u6743\u9650\u3002";
    if (status === 404) return "\u8FC1\u79FB\u8BB0\u5F55\u4E0D\u5B58\u5728\u6216\u5DF2\u4E0D\u53EF\u8BFB\u53D6\uFF0C\u8BF7\u91CD\u65B0\u751F\u6210\u9884\u89C8\u3002";
    if (status === 409) return "\u8FC1\u79FB\u72B6\u6001\u5DF2\u53D8\u5316\uFF0C\u8BF7\u91CD\u65B0\u751F\u6210\u9884\u89C8\u540E\u91CD\u8BD5\u3002";
    if (status === 400 || status === 405 || status === 422) return "\u8FC1\u79FB\u8BF7\u6C42\u65E0\u6548\uFF0C\u8BF7\u68C0\u67E5\u586B\u5199\u5185\u5BB9\u540E\u91CD\u8BD5\u3002";
    if (status >= 500) return "\u8FC1\u79FB\u670D\u52A1\u6682\u4E0D\u53EF\u7528\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002";
    return "\u8FC1\u79FB\u8BF7\u6C42\u5931\u8D25\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002";
  }
  function ownerHandoffErrorMessage(error, fallback) {
    if (error instanceof Error && error.userMessage) return error.message;
    const detail = error instanceof Error ? text(error.message) : "";
    return localOwnerHandoffMessages.has(detail) ? detail : fallback;
  }
  function scrubFrozenServerPlaceholders(page) {
    const marker = /\{\{|\{%/;
    const replacement = /\{\{[\s\S]*?\}\}|\{%[\s\S]*?%\}/g;
    [page, ...page.querySelectorAll("*")].forEach((element) => {
      [...element.attributes].forEach((attribute) => {
        if (marker.test(attribute.name)) element.removeAttribute(attribute.name);
        else if (marker.test(attribute.value)) element.setAttribute(attribute.name, attribute.value.replace(replacement, ""));
      });
    });
    const walker = document.createTreeWalker(page, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) node.nodeValue = (node.nodeValue || "").replace(replacement, "");
  }
  async function mountFrozenDonor(stage) {
    const response = await fetch(donorURL, { credentials: "same-origin" });
    if (!response.ok) throw requestFailure("\u8D1F\u8D23\u4EBA\u8FC1\u79FB\u9875\u9762\u6682\u4E0D\u53EF\u7528\uFF0C\u8BF7\u5237\u65B0\u540E\u91CD\u8BD5\u3002", response.status);
    const source = new DOMParser().parseFromString(await response.text(), "text/html");
    const page = source.querySelector("[data-owner-migration-page]");
    const style = source.querySelector("style");
    if (!page || !style) throw new Error("\u51BB\u7ED3\u9875\u9762\u4E0D\u542B\u8FC1\u79FB\u5DE5\u4F5C\u53F0");
    const cloned = page.cloneNode(true);
    scrubFrozenServerPlaceholders(cloned);
    stage.replaceChildren(style.cloneNode(true), cloned);
    const mounted = stage.querySelector("[data-owner-migration-page]");
    if (!mounted) throw new Error("\u51BB\u7ED3\u8FC1\u79FB\u9875\u9762\u672A\u6302\u8F7D");
    return mounted;
  }
  function query(root, selector) {
    const node = root.querySelector(selector);
    if (!node) throw new Error(`\u51BB\u7ED3\u9875\u9762\u7F3A\u5C11 ${selector}`);
    return node;
  }
  var OwnerStaffDirectory = class {
    byID = /* @__PURE__ */ new Map();
    constructor(initial) {
      initial.forEach((member) => this.upsert(member));
    }
    upsert(member) {
      const id = Number(member.ID);
      const userID = text(member.UserID);
      if (!Number.isSafeInteger(id) || id < 1 || !userID) return;
      this.byID.set(String(id), { ID: id, UserID: userID, DisplayName: text(member.DisplayName) || userID, Active: member.Active !== false });
    }
    get(rawID) {
      return this.byID.get(text(rawID));
    }
  };
  function ownerPickerFailure(response, body) {
    const detail = body && typeof body === "object" && "error" in body ? text(body.error) : "";
    return requestFailure(ownerHandoffRequestMessage(response.status, detail), response.status);
  }
  function ownerStaffRecord(member, kind) {
    return {
      source: "owner_migration.operation_members",
      staff_id: String(member.ID),
      user_id: member.UserID,
      display_name: member.DisplayName || member.UserID,
      active: member.Active,
      unavailable_reason: kind === "target" && !member.Active ? "\u76EE\u6807\u8D1F\u8D23\u4EBA\u5FC5\u987B\u662F\u5728\u804C\u5458\u5DE5\u3002" : void 0
    };
  }
  function unresolvedOwnerStaffRecord(rawID) {
    const id = text(rawID);
    return /^[1-9]\d*$/.test(id) ? {
      source: "owner_migration.operation_members",
      staff_id: id,
      user_id: "",
      display_name: `\u5458\u5DE5 #${id}`,
      unavailable_reason: "\u5F53\u524D\u5458\u5DE5\u76EE\u5F55\u6700\u591A\u8FD4\u56DE\u524D 100 \u9879\u6216\u641C\u7D22\u7ED3\u679C\uFF1B\u539F\u9009\u62E9\u4ECD\u4FDD\u7559\uFF0C\u4E0D\u80FD\u636E\u6B64\u5224\u5B9A\u5931\u6548\u3002"
    } : void 0;
  }
  function installPicker(root, directory) {
    const choose = (kind) => {
      const picker = window.AICRMStaffPicker;
      if (!picker || typeof picker.open !== "function") {
        const notice = root.querySelector("[data-workbench-notice]");
        if (notice) notice.textContent = "\u5458\u5DE5\u9009\u62E9\u5668\u65E0\u6CD5\u6253\u5F00\uFF1B\u5F53\u524D\u8D1F\u8D23\u4EBA\u8349\u7A3F\u672A\u4FEE\u6539\uFF0C\u8BF7\u5237\u65B0\u540E\u91CD\u8BD5\u3002";
        return;
      }
      const currentID = query(root, `[data-owner-userid="${kind}"]`).value;
      const current = directory.get(currentID);
      const initial = current ? ownerStaffRecord(current, kind) : unresolvedOwnerStaffRecord(currentID);
      const loadPage = async ({ query: search, signal }) => {
        const url = new URL("/api/admin/common/operation-members", window.location.origin);
        url.searchParams.set("scope", "owner_migration");
        url.searchParams.set("include_inactive", kind === "source" ? "true" : "false");
        url.searchParams.set("page_size", "100");
        if (text(search)) url.searchParams.set("q", text(search));
        const response = await fetch(url.toString(), { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
        const payload = await response.json().catch(() => ({}));
        if (signal.aborted) throw new DOMException("\u8D1F\u8D23\u4EBA\u76EE\u5F55\u8BFB\u53D6\u5DF2\u66FF\u6362", "AbortError");
        if (!response.ok) throw ownerPickerFailure(response, payload);
        const rawItems = payload && typeof payload === "object" && Array.isArray(payload.items) ? payload.items : null;
        if (!rawItems) throw new Error("\u5458\u5DE5\u76EE\u5F55\u54CD\u5E94\u4E0D\u5B8C\u6574\uFF0C\u8BF7\u91CD\u8BD5\u3002");
        const items = rawItems.flatMap((raw) => {
          if (signal.aborted) return [];
          const value = raw && typeof raw === "object" ? raw : {};
          const id = Number(value.staff_id);
          const userID = text(value.user_id);
          if (!Number.isSafeInteger(id) || id < 1 || !userID) return [];
          const member = { ID: id, UserID: userID, DisplayName: text(value.display_name) || userID, Active: value.active !== false };
          return [ownerStaffRecord(member, kind)];
        });
        return { items };
      };
      picker.open({
        title: kind === "source" ? "\u9009\u62E9\u539F\u8D1F\u8D23\u4EBA" : "\u9009\u62E9\u76EE\u6807\u8D1F\u8D23\u4EBA",
        source: "owner_migration.operation_members",
        scope: "owner_migration",
        mode: "single",
        limit: 1,
        selectedRecords: initial ? [initial] : [],
        directoryHint: "\u672C\u9875\u53EA\u663E\u793A\u524D 100 \u9879\u6216\u641C\u7D22\u7ED3\u679C\uFF1B\u672A\u51FA\u73B0\u7684\u539F\u9009\u62E9\u4ECD\u4FDD\u7559\uFF0C\u4E0D\u80FD\u636E\u6B64\u5224\u5B9A\u5931\u6548\u3002",
        loadPage,
        // This endpoint exposes an authorised local read only. Refresh merely
        // re-reads it; it never starts a Provider sync or mutation.
        refresh: async () => void 0,
        accessLossMessage: (error) => {
          const status = Number(error?.httpStatus);
          return status === 401 || status === 403 ? "\u8D1F\u8D23\u4EBA\u8FC1\u79FB\u5458\u5DE5\u76EE\u5F55\u6743\u9650\u5DF2\u5931\u6548\uFF1B\u5F53\u524D\u9009\u62E9\u4ECD\u53EF\u67E5\u770B\u6216\u53D6\u6D88\u3002" : void 0;
        },
        onCommit: ({ selected }) => {
          const picked = selected[0];
          const member = picked && Number.isSafeInteger(Number(picked.staff_id)) && Number(picked.staff_id) > 0 && text(picked.user_id) ? { ID: Number(picked.staff_id), UserID: text(picked.user_id), DisplayName: text(picked.display_name) || text(picked.user_id), Active: picked.active !== false } : void 0;
          if (!member || kind === "target" && !member.Active) throw new Error("\u6240\u9009\u5458\u5DE5\u4E0D\u518D\u53EF\u7528\u4E8E\u8D1F\u8D23\u4EBA\u8FC1\u79FB\uFF0C\u8BF7\u91CD\u65B0\u8BFB\u53D6\u76EE\u5F55\u3002");
          directory.upsert(member);
          query(root, `[data-owner-userid="${kind}"]`).value = String(member.ID);
          query(root, `[data-owner-label="${kind}"]`).value = member.DisplayName || member.UserID;
          root.dispatchEvent(new Event("owner-handoff-change"));
        }
      });
    };
    root.querySelectorAll("[data-owner-picker]").forEach((button) => button.addEventListener("click", () => choose(button.dataset.ownerPicker)));
  }
  function currentMode(root) {
    return query(root, "[data-include-wecom-transfer]").checked ? "wecom_then_crm" : "local_only";
  }
  function ownerID(root, kind) {
    return Number(query(root, `[data-owner-userid="${kind}"]`).value);
  }
  function ownerUserID(root, kind, directory) {
    return text(directory.get(ownerID(root, kind))?.UserID);
  }
  function selectedScope(root) {
    return query(root, 'input[name="scope_type"]:checked').value;
  }
  function transferStatusLabel(status) {
    return { 0: "\u672C\u5730\u8FC1\u79FB", 1: "\u4F01\u5FAE\u8F6C\u63A5\u5DF2\u5B8C\u6210", 2: "\u4F01\u5FAE\u8F6C\u63A5\u5904\u7406\u4E2D", 3: "\u5BA2\u6237\u62D2\u7EDD\u63A5\u66FF", 4: "\u76EE\u6807\u6210\u5458\u5BA2\u6237\u4E0A\u9650", 5: "\u672A\u627E\u5230\u4F01\u5FAE\u8F6C\u63A5\u8BB0\u5F55" }[status] || "\u4F01\u5FAE\u8F6C\u63A5\u72B6\u6001\u5F85\u786E\u8BA4";
  }
  function ownerMigrationStateLabel(state) {
    return {
      ready: "\u53EF\u8FC1\u79FB",
      skipped_by_file: "\u5DF2\u6309\u6587\u4EF6\u8DF3\u8FC7",
      not_under_source_owner: "\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4",
      not_found: "\u672A\u627E\u5230\u5BA2\u6237",
      conflict: "\u8FC1\u79FB\u51B2\u7A81",
      unresolved: "\u5F85\u6838\u5B9E",
      missing_external_userid: "\u7F3A\u5C11\u5BA2\u6237\u6807\u8BC6",
      invalid_move_flag: "\u8FC1\u79FB\u6807\u8BB0\u65E0\u6548",
      duplicate: "\u6587\u4EF6\u91CD\u590D",
      accepted: "\u5DF2\u53D7\u7406",
      queued: "\u6392\u961F\u4E2D",
      attempted: "\u6B63\u5728\u6267\u884C",
      executed: "\u5DF2\u6267\u884C",
      provider_accepted: "\u4F01\u5FAE\u5DF2\u53D7\u7406",
      final_failed: "\u6267\u884C\u5931\u8D25",
      outcome_unknown: "\u7ED3\u679C\u5F85\u6838\u5B9E",
      retryable_failed: "\u53EF\u91CD\u8BD5\u5931\u8D25",
      cancelled: "\u5DF2\u53D6\u6D88",
      reconciled: "\u5DF2\u6838\u5BF9",
      cas_conflict: "\u72B6\u6001\u51B2\u7A81",
      observed: "\u5DF2\u8BFB\u53D6\u7ED3\u679C"
    }[state] || "\u8FC1\u79FB\u72B6\u6001\u5F85\u786E\u8BA4";
  }
  function ownerMigrationReason(reason) {
    return {
      "external_userid is required": "\u7F3A\u5C11\u5BA2\u6237\u6807\u8BC6\u3002",
      "duplicate external_userid; first row is kept": "\u6587\u4EF6\u4E2D\u5B58\u5728\u91CD\u590D\u5BA2\u6237\u6807\u8BC6\uFF0C\u5DF2\u4FDD\u7559\u9996\u6B21\u51FA\u73B0\u7684\u8BB0\u5F55\u3002",
      "Excel marked skip": "\u5DF2\u6309\u6587\u4EF6\u6807\u8BB0\u8DF3\u8FC7\u3002",
      "no executable rows": "\u6CA1\u6709\u53EF\u6267\u884C\u8FC1\u79FB\u884C\u3002",
      "\u662F\u5426\u8FC1\u79FB\u5B57\u6BB5\u975E\u6CD5": "\u8FC1\u79FB\u6807\u8BB0\u65E0\u6548\u3002",
      "\u672A\u5F97\u5230\u8BE5\u884C\u7684\u5B89\u5168\u9884\u89C8\u7ED3\u679C": "\u672A\u5F97\u5230\u8BE5\u884C\u7684\u5B89\u5168\u9884\u89C8\u7ED3\u679C\u3002",
      "\u5DF2\u6309\u6587\u4EF6\u6807\u8BB0\u8DF3\u8FC7\u3002": "\u5DF2\u6309\u6587\u4EF6\u6807\u8BB0\u8DF3\u8FC7\u3002",
      "\u6CA1\u6709\u53EF\u6267\u884C\u8FC1\u79FB\u884C": "\u6CA1\u6709\u53EF\u6267\u884C\u8FC1\u79FB\u884C\u3002",
      "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4\uFF0C\u9884\u89C8\u9636\u6BB5\u5C06\u4E0D\u53EF\u6267\u884C": "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4\uFF0C\u9884\u89C8\u9636\u6BB5\u5C06\u4E0D\u53EF\u6267\u884C\u3002",
      "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4": "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4\u3002",
      "\u5F53\u524D\u8D1F\u8D23\u4EBAuserid\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4": "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4\u3002"
    }[reason] || "\u8FC1\u79FB\u539F\u56E0\u5F85\u786E\u8BA4\u3002";
  }
  function ownerMigrationModeLabel(mode) {
    return { local_only: "\u4EC5\u672C\u5730\u8FC1\u79FB", wecom_then_crm: "\u5148\u4F01\u5FAE\u8F6C\u63A5\u540E\u672C\u5730\u8FC1\u79FB" }[mode] || "\u8FC1\u79FB\u65B9\u5F0F\u5F85\u786E\u8BA4";
  }
  function downloadBlob(filename, blob) {
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    link.hidden = true;
    document.body.append(link);
    link.click();
    window.setTimeout(() => {
      link.remove();
      URL.revokeObjectURL(url);
    }, 1e3);
  }
  function downloadWorkbook(filename, headers, rows) {
    downloadBlob(filename, new Blob([ownerMigrationWorkbookXLSX(headers, rows)], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
  }
  function normalizeMoveFlag(value) {
    const normalized = text(value).toLowerCase();
    if ((/* @__PURE__ */ new Set(["", "\u662F", "y", "yes", "true", "1", "\u8FC1\u79FB"])).has(normalized)) return ["\u662F", true];
    if ((/* @__PURE__ */ new Set(["\u5426", "n", "no", "false", "0", "\u4E0D\u8FC1\u79FB"])).has(normalized)) return ["\u5426", true];
    return [text(value), false];
  }
  function normalizeImportedRows(rawRows, sourceUserID) {
    const seen = /* @__PURE__ */ new Set();
    return rawRows.map((row, index) => {
      const external = text(row[0]);
      const [moveFlag, validFlag] = normalizeMoveFlag(text(row[1]));
      const current = text(row[2]);
      let parseStatus = "parsed";
      let parseReason = "";
      if (!external) {
        parseStatus = "missing_external_userid";
        parseReason = "external_userid is required";
      } else if (!validFlag) {
        parseStatus = "invalid_move_flag";
        parseReason = "\u662F\u5426\u8FC1\u79FB\u5B57\u6BB5\u975E\u6CD5";
      } else if (seen.has(external)) {
        parseStatus = "duplicate";
        parseReason = "duplicate external_userid; first row is kept";
      } else {
        seen.add(external);
        if (current && current !== sourceUserID) parseReason = "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4\uFF0C\u9884\u89C8\u9636\u6BB5\u5C06\u4E0D\u53EF\u6267\u884C";
      }
      return { Line: index + 2, ExternalUserID: external, MoveFlag: moveFlag, CurrentOwnerUserID: current, CustomerDisplayName: text(row[3]), Remark: text(row[4]), ParseStatus: parseStatus, ParseReason: parseReason };
    });
  }
  function importStats(rows) {
    const unique = new Set(rows.filter((row) => row.ExternalUserID && row.ParseStatus !== "duplicate").map((row) => row.ExternalUserID));
    return {
      total_rows: rows.length,
      unique_external_userids: unique.size,
      marked_move: rows.filter((row) => row.ParseStatus === "parsed" && row.MoveFlag === "\u662F").length,
      marked_skip: rows.filter((row) => row.ParseStatus === "parsed" && row.MoveFlag === "\u5426").length,
      duplicate_rows: rows.filter((row) => row.ParseStatus === "duplicate").length,
      invalid_rows: rows.filter((row) => row.ParseStatus === "missing_external_userid" || row.ParseStatus === "invalid_move_flag").length
    };
  }
  function displayFromServer(row, item) {
    const mappedState = row.State === "conflict" ? "not_under_source_owner" : row.State === "unresolved" ? "not_found" : row.State;
    return { Line: item.Line, ExternalUserID: row.ExternalUserID || item.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName || item.CustomerDisplayName, MoveFlag: item.MoveFlag, CurrentOwnerUserID: row.CurrentOwnerUserID || item.CurrentOwnerUserID, Remark: item.Remark, State: mappedState, Reason: row.Reason || "\u5DF2\u901A\u8FC7\u9884\u89C8\u6821\u9A8C", CustomerID: row.CustomerID };
  }
  function previewDisplayRows(preview, scope, imported, sourceUserID) {
    if (scope !== "excel_include") return preview.Rows.map((row) => ({ Line: row.Line, ExternalUserID: row.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName, MoveFlag: "\u662F", CurrentOwnerUserID: row.CurrentOwnerUserID, Remark: "", State: row.State, Reason: row.Reason || "\u5DF2\u901A\u8FC7\u9884\u89C8\u6821\u9A8C", CustomerID: row.CustomerID }));
    const serverRows = new Map(preview.Rows.map((row) => [row.ExternalUserID, row]));
    return imported.map((item) => {
      if (item.ParseStatus !== "parsed") return { ...item, State: item.ParseStatus, Reason: item.ParseReason };
      if (item.MoveFlag === "\u5426") return { ...item, State: "skipped_by_file", Reason: "\u5DF2\u6309\u6587\u4EF6\u6807\u8BB0\u8DF3\u8FC7\u3002" };
      if (item.CurrentOwnerUserID && item.CurrentOwnerUserID !== sourceUserID) return { ...item, State: "not_under_source_owner", Reason: "\u5F53\u524D\u8D1F\u8D23\u4EBA\u6807\u8BC6\u4E0E\u9009\u62E9\u7684\u539F\u8D1F\u8D23\u4EBA\u4E0D\u4E00\u81F4" };
      const server = serverRows.get(item.ExternalUserID);
      if (!server) return { ...item, State: "not_found", Reason: "\u672A\u5F97\u5230\u8BE5\u884C\u7684\u5B89\u5168\u9884\u89C8\u7ED3\u679C" };
      return displayFromServer(server, item);
    });
  }
  function renderRows(root, rows, scope, source, target) {
    const ready = rows.filter((row) => row.State === "ready").length;
    const skipped = rows.filter((row) => row.State === "skipped_by_file").length;
    const blocked = rows.length - ready - skipped;
    query(root, "[data-preview-basic]").textContent = `${scope === "excel_include" ? "Excel \u6307\u5B9A\u540D\u5355" : "\u5168\u90E8\u5BA2\u6237"} \xB7 \u539F\u8D1F\u8D23\u4EBA #${source} \u2192 \u76EE\u6807\u8D1F\u8D23\u4EBA #${target} \xB7 ${ready} \u4E2A\u53EF\u8FC1\u79FB\u5BA2\u6237\uFF1B${blocked} \u4E2A\u4E0D\u53EF\u8FC1\u79FB\u3002`;
    const values = { total_rows: rows.length, unique_external_userids: new Set(rows.map((row) => row.ExternalUserID).filter(Boolean)).size, ready, skipped_by_file: skipped, blocked, crm_updates: ready };
    Object.entries(values).forEach(([name, value]) => {
      const node = root.querySelector(`[data-preview-stat="${name}"]`);
      if (node) node.textContent = String(value);
    });
    query(root, "[data-preview-rows]").innerHTML = rows.map((row) => `<tr><td>${row.Line}</td><td><code>${esc(row.ExternalUserID)}</code></td><td>${esc(row.CustomerDisplayName)}</td><td>${esc(row.MoveFlag)}</td><td>${esc(row.CurrentOwnerUserID)}</td><td><span class="owner-migration-status owner-migration-status--${row.State === "ready" ? "ready" : row.State === "skipped_by_file" ? "skip" : "block"}">${esc(ownerMigrationStateLabel(row.State))}</span></td><td>${esc(ownerMigrationReason(row.Reason))}</td></tr>`).join("") || '<tr><td colspan="7" class="owner-migration-empty">\u5F53\u524D\u8303\u56F4\u6CA1\u6709\u5019\u9009\u5BA2\u6237\u3002</td></tr>';
    query(root, "[data-download-errors]").disabled = blocked === 0;
    query(root, "[data-execute]").disabled = ready === 0;
  }
  function renderPreview(root, preview, scope, imported, sourceUserID) {
    query(root, "[data-preview-empty]").hidden = true;
    query(root, "[data-preview-content]").hidden = false;
    const rows = previewDisplayRows(preview, scope, imported, sourceUserID);
    renderRows(root, rows, scope, ownerID(root, "source"), ownerID(root, "target"));
    query(root, "[data-confirm-phrase-display]").textContent = preview.ConfirmationPhrase;
    return rows;
  }
  function renderBatch(root, batch) {
    root.dataset.ownerHandoffBatchId = batch.ID;
    query(root, "[data-execution-log]").textContent = [
      `\u8FC1\u79FB\u6279\u6B21\uFF1A${batch.ID}`,
      `\u8FC1\u79FB\u65B9\u5F0F\uFF1A${ownerMigrationModeLabel(batch.Mode)}`,
      `\u6279\u6B21\u72B6\u6001\uFF1A${ownerMigrationStateLabel(batch.State)}`,
      ...(batch.Lines || []).map((line) => `\u7B2C ${line.Line} \u884C\uFF0C\u5BA2\u6237 #${line.CustomerID}\uFF1A${ownerMigrationStateLabel(line.State)}\uFF1B\u4F01\u5FAE\u8F6C\u63A5\uFF1A${transferStatusLabel(line.TransferStatus)}`)
    ].join("\n");
  }
  async function boot() {
    const stage = document.querySelector("[data-owner-handoff-host]");
    if (!stage) return;
    stage.dataset.ownerHandoffInit = "mounting";
    try {
      const root = await mountFrozenDonor(stage);
      stage.dataset.ownerHandoffInit = "donor_loaded";
      const context = await api("/api/admin/customers/owner-handoffs/context");
      stage.dataset.ownerHandoffInit = "context_loaded";
      const staffDirectory = new OwnerStaffDirectory(context.staff || []);
      installPicker(root, staffDirectory);
      query(root, '[data-owner-label="source"]').value = "";
      query(root, '[data-owner-label="target"]').value = "";
      query(root, '[data-owner-userid="source"]').value = "";
      query(root, '[data-owner-userid="target"]').value = "";
      query(root, "#operator").value = context.operator || "\u5F53\u524D\u767B\u5F55\u7BA1\u7406\u5458";
      query(root, "[data-transfer-welcome-msg]").value = "\u60A8\u597D\uFF0C\u540E\u7EED\u5C06\u7531\u65B0\u7684\u670D\u52A1\u540C\u4E8B\u7EE7\u7EED\u4E3A\u60A8\u670D\u52A1\u3002";
      query(root, "[data-include-wecom-transfer]").checked = true;
      query(root, "[data-wecom-pill]").textContent = "\u4F01\u5FAE\u8F6C\u63A5\uFF1A\u9ED8\u8BA4\u5F00\u542F";
      query(root, "[data-local-only-warning]").hidden = true;
      query(root, "[data-import-file]").setAttribute("accept", ".xlsx,.xls,.csv");
      const updateWelcomeCount = () => {
        query(root, "[data-welcome-count]").textContent = `${text(query(root, "[data-transfer-welcome-msg]").value).length} \u5B57`;
      };
      updateWelcomeCount();
      let preview;
      let batch;
      let fileExternalIDs = [];
      let importedRows = [];
      let displayedRows = [];
      const notice = query(root, "[data-workbench-notice]");
      const setNotice = (value, kind = "") => {
        notice.textContent = value;
        notice.className = `owner-migration-hint ${kind}`;
      };
      const reset = () => {
        preview = void 0;
        batch = void 0;
        displayedRows = [];
        delete root.dataset.ownerHandoffBatchId;
        query(root, "[data-preview-empty]").hidden = false;
        query(root, "[data-preview-content]").hidden = true;
        query(root, "[data-confirm-phrase-input]").value = "";
        query(root, "[data-execute]").disabled = true;
        query(root, "[data-download-errors]").disabled = true;
        query(root, "[data-download-result]").disabled = true;
        const transferReader = root.querySelector("[data-read-transfer-result]");
        if (transferReader) transferReader.disabled = true;
        query(root, "[data-execution-log]").textContent = "\u5C1A\u672A\u6267\u884C\u3002";
      };
      const updateWeComPresentation = () => {
        const enabled = query(root, "[data-include-wecom-transfer]").checked;
        const pill = query(root, "[data-wecom-pill]");
        pill.textContent = enabled ? "\u4F01\u5FAE\u8F6C\u63A5\uFF1A\u9ED8\u8BA4\u5F00\u542F" : "\u4F01\u5FAE\u8F6C\u63A5\uFF1A\u5DF2\u5173\u95ED";
        pill.classList.toggle("owner-migration-pill--success", enabled);
        pill.classList.toggle("owner-migration-pill--warn", !enabled);
        query(root, "[data-local-only-warning]").hidden = enabled;
      };
      root.addEventListener("owner-handoff-change", reset);
      root.querySelectorAll("[data-scope-segment]").forEach((segment) => segment.addEventListener("click", () => {
        const scope = segment.dataset.scopeSegment || "all";
        root.querySelectorAll("[data-scope-segment]").forEach((value) => value.classList.toggle("is-active", value === segment));
        query(root, "[data-mode-pill]").textContent = scope === "excel_include" ? "\u6A21\u5F0F\uFF1AExcel \u6307\u5B9A\u540D\u5355" : "\u6A21\u5F0F\uFF1A\u5168\u91CF\u8FC1\u79FB";
        query(root, `input[name="scope_type"][value="${scope}"]`).checked = true;
        query(root, "[data-excel-panel]").hidden = scope !== "excel_include";
        reset();
      }));
      query(root, "[data-upload-file]").addEventListener("click", async () => {
        try {
          const source = ownerID(root, "source");
          const target = ownerID(root, "target");
          const sourceUserID = ownerUserID(root, "source", staffDirectory);
          if (!source || !target || source === target || !sourceUserID) throw new Error("\u8BF7\u5148\u9009\u62E9\u4E0D\u540C\u7684\u539F\u8D1F\u8D23\u4EBA\u548C\u76EE\u6807\u8D1F\u8D23\u4EBA");
          const file = query(root, "[data-import-file]").files?.[0];
          if (!file) throw new Error("\u8BF7\u9009\u62E9\u5305\u542B\u65E7\u6A21\u677F\u4E94\u5217\u7684 XLSX\u3001XLS \u6216 CSV \u6587\u4EF6");
          const rawRows = await ownerMigrationRowsFromFile(file);
          const headers = rawRows.shift() || [];
          const expectedHeaders = ["external_userid", "\u662F\u5426\u8FC1\u79FB", "\u5F53\u524D\u8D1F\u8D23\u4EBAuserid", "\u5BA2\u6237\u5907\u6CE8\u540D", "\u5907\u6CE8"];
          if (headers.length !== expectedHeaders.length || headers.some((header, index) => text(header) !== expectedHeaders[index])) throw new Error(`\u7B2C\u4E00\u884C\u5FC5\u987B\u4E14\u53EA\u80FD\u662F\uFF1A${expectedHeaders.join("\u3001")}`);
          if (rawRows.some((row) => row.length !== expectedHeaders.length)) throw new Error("\u6BCF\u4E00\u884C\u5FC5\u987B\u5305\u542B\u65E7\u6A21\u677F\u7684\u4E94\u5217");
          importedRows = normalizeImportedRows(rawRows, sourceUserID);
          fileExternalIDs = importedRows.filter((row) => row.ParseStatus === "parsed" && row.MoveFlag === "\u662F" && row.ExternalUserID && (!row.CurrentOwnerUserID || row.CurrentOwnerUserID === sourceUserID)).map((row) => row.ExternalUserID);
          const stats = importStats(importedRows);
          query(root, "[data-import-summary]").hidden = false;
          query(root, "[data-import-filename]").textContent = file.name;
          Object.entries(stats).forEach(([name, value]) => {
            const node = root.querySelector(`[data-import-stat="${name}"]`);
            if (node) node.textContent = String(value);
          });
          reset();
          setNotice("\u65E7\u6A21\u677F\u540D\u5355\u5DF2\u89E3\u6790\uFF1B\u9884\u89C8\u4F1A\u4FDD\u7559\u6BCF\u4E00\u884C\u7684\u6807\u8BB0\u3001\u91CD\u590D\u548C\u8D1F\u8D23\u4EBA\u6821\u9A8C\u7ED3\u679C\u3002", "ok");
        } catch (error) {
          setNotice(ownerHandoffErrorMessage(error, "\u6587\u4EF6\u89E3\u6790\u5931\u8D25\uFF0C\u8BF7\u68C0\u67E5\u6587\u4EF6\u540E\u91CD\u8BD5\u3002"), "error");
        }
      });
      root.querySelectorAll('input[name="scope_type"]').forEach((input) => input.addEventListener("change", reset));
      query(root, "[data-include-wecom-transfer]").addEventListener("change", () => {
        updateWeComPresentation();
        reset();
      });
      query(root, "[data-transfer-welcome-msg]").addEventListener("input", () => {
        updateWelcomeCount();
        reset();
      });
      query(root, "[data-download-template]").addEventListener("click", () => {
        downloadBlob("owner_migration_template.xlsx", new Blob([ownerMigrationTemplateXLSX()], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
      });
      query(root, "[data-preview]").addEventListener("click", async () => {
        try {
          const source = ownerID(root, "source");
          const target = ownerID(root, "target");
          const sourceUserID = ownerUserID(root, "source", staffDirectory);
          if (!source || !target || source === target || !sourceUserID) throw new Error("\u8BF7\u5148\u9009\u62E9\u4E0D\u540C\u7684\u539F\u8D1F\u8D23\u4EBA\u548C\u76EE\u6807\u8D1F\u8D23\u4EBA");
          const scope = selectedScope(root);
          if (scope === "excel_include" && !importedRows.length) throw new Error("\u8BF7\u5148\u4E0A\u4F20\u65E7\u6A21\u677F\u540D\u5355");
          if (scope === "excel_include" && !fileExternalIDs.length) {
            displayedRows = importedRows.map((row) => row.ParseStatus === "parsed" && row.MoveFlag === "\u5426" ? { ...row, State: "skipped_by_file", Reason: "\u5DF2\u6309\u6587\u4EF6\u6807\u8BB0\u8DF3\u8FC7\u3002" } : { ...row, State: row.ParseStatus, Reason: row.ParseReason || "\u6CA1\u6709\u53EF\u6267\u884C\u8FC1\u79FB\u884C" });
            query(root, "[data-preview-empty]").hidden = true;
            query(root, "[data-preview-content]").hidden = false;
            renderRows(root, displayedRows, scope, source, target);
            setNotice("\u6587\u4EF6\u6CA1\u6709\u53EF\u6267\u884C\u8FC1\u79FB\u884C\uFF0C\u5DF2\u4FDD\u7559\u9010\u884C\u6821\u9A8C\u7ED3\u679C\uFF0C\u4E0D\u80FD\u786E\u8BA4\u6267\u884C\u3002", "ok");
            return;
          }
          preview = await api("/api/admin/customers/owner-handoffs/previews", { method: "POST", body: JSON.stringify({ mode: currentMode(root), scope, source_staff_id: source, target_staff_id: target, customer_ids: [], external_userids: scope === "excel_include" ? fileExternalIDs : [], welcome_message: query(root, "[data-transfer-welcome-msg]").value, confirmation_phrase: `\u786E\u8BA4\u5C06\u5F53\u524D\u5019\u9009\u5BA2\u6237\u8FC1\u79FB\u5230 ${target}`, idempotency_key: key() }) });
          displayedRows = renderPreview(root, preview, scope, importedRows, sourceUserID);
          setNotice("\u9884\u89C8\u5DF2\u751F\u6210\uFF0C\u8BF7\u9010\u5B57\u8F93\u5165\u786E\u8BA4\u77ED\u8BED\u3002", "ok");
        } catch (error) {
          setNotice(ownerHandoffErrorMessage(error, "\u9884\u89C8\u5931\u8D25\uFF0C\u8BF7\u68C0\u67E5\u586B\u5199\u5185\u5BB9\u540E\u91CD\u8BD5\u3002"), "error");
        }
      });
      query(root, "[data-execute]").addEventListener("click", async () => {
        try {
          if (!preview) throw new Error("\u8BF7\u5148\u751F\u6210\u9884\u89C8");
          const phrase = query(root, "[data-confirm-phrase-input]").value;
          if (phrase !== preview.ConfirmationPhrase) throw new Error("\u786E\u8BA4\u77ED\u8BED\u4E0D\u5339\u914D");
          batch = await api("/api/admin/customers/owner-handoffs/confirm", { method: "POST", body: JSON.stringify({ preview_id: preview.ID, preview_hash: preview.Hash, confirmation_phrase: phrase, idempotency_key: key() }) });
          renderBatch(root, batch);
          query(root, "[data-download-result]").disabled = false;
          readTransfer.disabled = false;
          setNotice("\u8FC1\u79FB\u5DF2\u53D7\u7406\uFF1B\u7ED3\u679C\u5BFC\u51FA\u548C\u4F01\u5FAE\u7ED3\u679C\u8BFB\u53D6\u4F1A\u663E\u793A\u6BCF\u4E00\u884C\u5B9E\u9645\u72B6\u6001\u3002", "ok");
        } catch (error) {
          setNotice(ownerHandoffErrorMessage(error, "\u6267\u884C\u5931\u8D25\uFF0C\u8BF7\u91CD\u65B0\u751F\u6210\u9884\u89C8\u540E\u91CD\u8BD5\u3002"), "error");
        }
      });
      query(root, "[data-reset-workbench]").addEventListener("click", reset);
      query(root, "[data-download-errors]").addEventListener("click", () => {
        const blocked = displayedRows.filter((row) => row.State !== "ready" && row.State !== "skipped_by_file");
        if (!blocked.length) return;
        downloadWorkbook("owner_migration_blocked_rows.xlsx", ["\u884C\u53F7", "external_userid", "\u5BA2\u6237\u5907\u6CE8\u540D", "Excel \u6807\u8BB0", "\u5F53\u524D\u8D1F\u8D23\u4EBAuserid", "\u5907\u6CE8", "\u72B6\u6001", "\u539F\u56E0"], blocked.map((row) => [String(row.Line), row.ExternalUserID, row.CustomerDisplayName, row.MoveFlag, row.CurrentOwnerUserID, row.Remark, ownerMigrationStateLabel(row.State), ownerMigrationReason(row.Reason)]));
      });
      query(root, "[data-download-result]").addEventListener("click", async () => {
        if (!batch) {
          setNotice("\u8BF7\u5148\u6267\u884C\u8FC1\u79FB\uFF0C\u518D\u5BFC\u51FA\u7ED3\u679C\u660E\u7EC6\u3002", "error");
          return;
        }
        try {
          batch = await api(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}`);
          renderBatch(root, batch);
          const rowsByCustomer = new Map(displayedRows.filter((row) => row.CustomerID).map((row) => [row.CustomerID, row]));
          downloadWorkbook("owner_migration_result.xlsx", ["\u884C\u53F7", "external_userid", "\u5BA2\u6237\u5907\u6CE8\u540D", "\u5F53\u524D\u8D1F\u8D23\u4EBAuserid", "\u5907\u6CE8", "\u8FC1\u79FB\u72B6\u6001", "\u4F01\u5FAE\u8F6C\u63A5\u72B6\u6001"], (batch.Lines || []).map((line) => {
            const row = rowsByCustomer.get(line.CustomerID);
            return [String(row?.Line || line.Line), row?.ExternalUserID || "", row?.CustomerDisplayName || "", row?.CurrentOwnerUserID || "", row?.Remark || "", ownerMigrationStateLabel(line.State), transferStatusLabel(line.TransferStatus)];
          }));
          setNotice("\u5DF2\u5BFC\u51FA\u5F53\u524D\u6279\u6B21\u7ED3\u679C\u660E\u7EC6\u3002", "ok");
        } catch (error) {
          setNotice(ownerHandoffErrorMessage(error, "\u7ED3\u679C\u6682\u4E0D\u53EF\u8BFB\u53D6\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002"), "error");
        }
      });
      query(root, "[data-download-result]").textContent = "\u4E0B\u8F7D\u7ED3\u679C\u660E\u7EC6";
      const readTransfer = document.createElement("button");
      readTransfer.type = "button";
      readTransfer.className = "owner-migration-btn";
      readTransfer.dataset.readTransferResult = "";
      readTransfer.textContent = "\u8BFB\u53D6\u4F01\u5FAE\u8F6C\u63A5\u7ED3\u679C";
      readTransfer.disabled = true;
      query(root, "[data-download-result]").parentElement?.append(readTransfer);
      readTransfer.addEventListener("click", async () => {
        if (!batch) return;
        stage.dataset.ownerHandoffTransferResultStatus = "pending";
        try {
          batch = await api(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}/transfer-result`, { method: "POST", body: JSON.stringify({ idempotency_key: key() }) });
          stage.dataset.ownerHandoffTransferResultStatus = "ok";
          renderBatch(root, batch);
          setNotice("\u5DF2\u8BFB\u53D6\u4F01\u5FAE\u8F6C\u63A5\u7ED3\u679C\u3002", "ok");
        } catch (error) {
          const status = error && typeof error === "object" && "httpStatus" in error && typeof error.httpStatus === "number" ? error.httpStatus : 0;
          stage.dataset.ownerHandoffTransferResultStatus = status > 0 ? `http_${status}` : "error";
          setNotice(ownerHandoffErrorMessage(error, "\u4F01\u5FAE\u8F6C\u63A5\u7ED3\u679C\u6682\u4E0D\u53EF\u8BFB\u53D6\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002"), "error");
        }
      });
      query(root, 'input[name="scope_type"][value="all"]').checked = true;
      root.querySelector('[data-scope-segment="all"]')?.classList.add("is-active");
      root.querySelector('[data-scope-segment="excel_include"]')?.classList.remove("is-active");
      query(root, "[data-excel-panel]").hidden = true;
      query(root, "[data-mode-pill]").textContent = "\u6A21\u5F0F\uFF1A\u5168\u91CF\u8FC1\u79FB";
      updateWeComPresentation();
      reset();
      setNotice("\u539F\u8D1F\u8D23\u4EBA\u53EF\u542B\u505C\u7528\u5458\u5DE5\uFF0C\u76EE\u6807\u8D1F\u8D23\u4EBA\u53EA\u5217\u5728\u804C\u5458\u5DE5\u3002");
      stage.dataset.ownerHandoffInit = "ready";
    } catch (error) {
      const phase = stage.dataset.ownerHandoffInit || "mounting";
      const failure = error;
      stage.dataset.ownerHandoffInit = phase === "mounting" ? "donor_error" : phase === "donor_loaded" ? "context_error" : "host_error";
      if (failure.httpStatus) stage.dataset.ownerHandoffInitStatus = String(failure.httpStatus);
      else delete stage.dataset.ownerHandoffInitStatus;
      stage.textContent = "\u8D1F\u8D23\u4EBA\u8FC1\u79FB\u9875\u9762\u4E0D\u53EF\u7528\u3002";
    }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => {
    void boot();
  });
  else void boot();
})();
