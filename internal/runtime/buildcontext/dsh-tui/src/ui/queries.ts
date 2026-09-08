// The main queries come from the exact grammar package versions used to build
// tree-sitter-wasms 0.1.13. CSS and HTML use small local queries.
const bash = `
[(string) (raw_string) (heredoc_body) (heredoc_start)] @string
(command_name) @function
(variable_name) @property
["case" "do" "done" "elif" "else" "esac" "export" "fi" "for" "function" "if" "in" "select" "then" "unset" "until" "while"] @keyword
(comment) @comment
(function_definition name: (word) @function)
(file_descriptor) @number
[(command_substitution) (process_substitution) (expansion)] @embedded
["$" "&&" ">" ">>" "<" "|"] @operator
((command (_) @constant) (#match? @constant "^-"))
`

const c = `
["break" "case" "const" "continue" "default" "do" "else" "enum" "extern" "for" "if" "inline" "return" "sizeof" "static" "struct" "switch" "typedef" "union" "volatile" "while"] @keyword
["#define" "#elif" "#else" "#endif" "#if" "#ifdef" "#ifndef" "#include"] @keyword
(preproc_directive) @keyword
["--" "-" "-=" "->" "=" "!=" "*" "&" "&&" "+" "++" "+=" "<" "==" ">" "||"] @operator
["." ";"] @delimiter
(string_literal) @string
(system_lib_string) @string
(null) @constant
(number_literal) @number
(char_literal) @number
(call_expression function: (identifier) @function)
(call_expression function: (field_expression field: (field_identifier) @function))
(function_declarator declarator: (identifier) @function)
(preproc_function_def name: (identifier) @function.special)
(field_identifier) @property
(statement_identifier) @label
(type_identifier) @type
(primitive_type) @type
(sized_type_specifier) @type
((identifier) @constant (#match? @constant "^[A-Z][A-Z\\d_]*$"))
(identifier) @variable
(comment) @comment
`

const cpp = `${c}
(call_expression function: (qualified_identifier name: (identifier) @function))
(template_function name: (identifier) @function)
(template_method name: (field_identifier) @function)
(function_declarator declarator: (qualified_identifier name: (identifier) @function))
(function_declarator declarator: (field_identifier) @function)
((namespace_identifier) @type (#match? @type "^[A-Z]"))
(auto) @type
(this) @variable.builtin
(null "nullptr" @constant)
["catch" "class" "co_await" "co_return" "co_yield" "constexpr" "constinit" "consteval" "delete" "explicit" "final" "friend" "mutable" "namespace" "noexcept" "new" "override" "private" "protected" "public" "template" "throw" "try" "typename" "using" "virtual" "concept" "requires"] @keyword
(raw_string_literal) @string
`

const css = `
(comment) @comment
(string_value) @string
(integer_value) @number
(float_value) @number
(color_value) @constant
(property_name) @property
(tag_name) @tag
(attribute_name) @attribute
(class_name) @type
(id_name) @type.definition
(function_name) @function
`

const go = `
(call_expression function: (identifier) @function.builtin (.match? @function.builtin "^(append|cap|close|complex|copy|delete|imag|len|make|new|panic|print|println|real|recover)$"))
(call_expression function: (identifier) @function)
(call_expression function: (selector_expression field: (field_identifier) @function.method))
(function_declaration name: (identifier) @function)
(method_declaration name: (field_identifier) @function.method)
(type_identifier) @type
(field_identifier) @property
(identifier) @variable
["--" "-" "-=" ":=" "!" "!=" "..." "*" "*=" "/" "/=" "&" "&&" "&=" "%" "%=" "^" "^=" "+" "++" "+=" "<-" "<" "<<" "<<=" "<=" "=" "==" ">" ">=" ">>" ">>=" "|" "|=" "||" "~"] @operator
["break" "case" "chan" "const" "continue" "default" "defer" "else" "fallthrough" "for" "func" "go" "goto" "if" "import" "interface" "map" "package" "range" "return" "select" "struct" "switch" "type" "var"] @keyword
[(interpreted_string_literal) (raw_string_literal) (rune_literal)] @string
(escape_sequence) @escape
[(int_literal) (float_literal) (imaginary_literal)] @number
[(true) (false) (nil) (iota)] @constant.builtin
(comment) @comment
`

const html = `
(comment) @comment
(tag_name) @tag
(attribute_name) @attribute
(quoted_attribute_value) @string
(attribute_value) @string
`

const json = `
(pair key: (_) @string.special.key)
(string) @string
(number) @number
[(null) (true) (false)] @constant.builtin
(escape_sequence) @escape
(comment) @comment
`

const python = `
(identifier) @variable
((identifier) @constructor (#match? @constructor "^[A-Z]"))
((identifier) @constant (#match? @constant "^[A-Z][A-Z_]*$"))
(decorator) @function
(call function: (attribute attribute: (identifier) @function.method))
(call function: (identifier) @function)
((call function: (identifier) @function.builtin) (#match? @function.builtin "^(abs|all|any|ascii|bin|bool|breakpoint|bytearray|bytes|callable|chr|classmethod|compile|complex|delattr|dict|dir|divmod|enumerate|eval|exec|filter|float|format|frozenset|getattr|globals|hasattr|hash|help|hex|id|input|int|isinstance|issubclass|iter|len|list|locals|map|max|memoryview|min|next|object|oct|open|ord|pow|print|property|range|repr|reversed|round|set|setattr|slice|sorted|staticmethod|str|sum|super|tuple|type|vars|zip|__import__)$"))
(function_definition name: (identifier) @function)
(attribute attribute: (identifier) @property)
(type (identifier) @type)
[(none) (true) (false)] @constant.builtin
[(integer) (float)] @number
(comment) @comment
(string) @string
(escape_sequence) @escape
(interpolation "{" @punctuation.special "}" @punctuation.special) @embedded
["-" "-=" "!=" "*" "**" "**=" "*=" "/" "//" "//=" "/=" "&" "&=" "%" "%=" "^" "^=" "+" "->" "+=" "<" "<<" "<<=" "<=" "<>" "=" ":=" "==" ">" ">=" ">>" ">>=" "|" "|=" "~" "@=" "and" "in" "is" "not" "or"] @operator
["as" "assert" "async" "await" "break" "class" "continue" "def" "del" "elif" "else" "except" "exec" "finally" "for" "from" "global" "if" "import" "lambda" "nonlocal" "pass" "print" "raise" "return" "try" "while" "with" "yield" "match" "case"] @keyword
`

const rust = `
((identifier) @constant (#match? @constant "^[A-Z][A-Z\\d_]+$'"))
((scoped_identifier path: (identifier) @type) (#match? @type "^[A-Z]"))
((scoped_identifier path: (scoped_identifier name: (identifier) @type)) (#match? @type "^[A-Z]"))
((scoped_type_identifier path: (identifier) @type) (#match? @type "^[A-Z]"))
((scoped_type_identifier path: (scoped_identifier name: (identifier) @type)) (#match? @type "^[A-Z]"))
((identifier) @constructor (#match? @constructor "^[A-Z]"))
(struct_pattern type: (scoped_type_identifier name: (type_identifier) @constructor))
(call_expression function: (identifier) @function)
(call_expression function: (field_expression field: (field_identifier) @function.method))
(call_expression function: (scoped_identifier "::" name: (identifier) @function))
(generic_function function: (identifier) @function)
(generic_function function: (scoped_identifier name: (identifier) @function))
(generic_function function: (field_expression field: (field_identifier) @function.method))
(macro_invocation macro: (identifier) @function.macro "!" @function.macro)
(function_item (identifier) @function)
(function_signature_item (identifier) @function)
(type_identifier) @type
(primitive_type) @type.builtin
(field_identifier) @property
(line_comment) @comment
(block_comment) @comment
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
(type_arguments "<" @punctuation.bracket ">" @punctuation.bracket)
(type_parameters "<" @punctuation.bracket ">" @punctuation.bracket)
["::" ":" "." "," ";"] @punctuation.delimiter
(parameter (identifier) @variable.parameter)
(lifetime (identifier) @label)
["as" "async" "await" "break" "const" "continue" "default" "dyn" "else" "enum" "extern" "fn" "for" "if" "impl" "in" "let" "loop" "macro_rules!" "match" "mod" "move" "pub" "ref" "return" "static" "struct" "trait" "type" "union" "unsafe" "use" "where" "while"] @keyword
[(crate) (mutable_specifier) (use_list (self)) (scoped_use_list (self)) (scoped_identifier (self)) (super)] @keyword
(self) @variable.builtin
[(char_literal) (string_literal) (raw_string_literal)] @string
[(boolean_literal) (integer_literal) (float_literal)] @constant.builtin
(escape_sequence) @escape
(attribute_item) @attribute
(inner_attribute_item) @attribute
["*" "&" "'"] @operator
`

export const queries: Record<string, string> = { bash, c, cpp, css, go, html, json, python, rust }
