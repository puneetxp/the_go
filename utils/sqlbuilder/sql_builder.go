package sqlbuilder

import (
	"fmt"
	"strings"
)

type JoinSpec struct {
	Alias      string
	Table      string
	LocalKey   string
	ForeignKey string
	Cols       []string
	Prefix     string
}

type JoinQueryResult struct {
	SQL         string
	Placeholders []interface{}
	SelectParts []string
}

func BuildSelectList(alias string, cols []string, prefix string) []string {
	if len(cols) == 0 {
		return []string{fmt.Sprintf("%s.*", alias)}
	}
	res := make([]string, len(cols))
	for i, col := range cols {
		as := col
		if prefix != "" {
			as = fmt.Sprintf("%s__%s", prefix, col)
		}
		res[i] = fmt.Sprintf("%s.`%s` AS %s", alias, col, as)
	}
	return res
}

func BuildJoinQuery(
	baseTable string,
	baseAlias string,
	baseCols []string,
	joins []JoinSpec,
	where map[string]interface{},
) JoinQueryResult {
	selectParts := BuildSelectList(baseAlias, baseCols, "")
	var joinSQL strings.Builder

	for _, join := range joins {
		relSelect := BuildSelectList(join.Alias, join.Cols, join.Prefix)
		selectParts = append(selectParts, relSelect...)
		joinSQL.WriteString(fmt.Sprintf(" LEFT JOIN %s %s ON %s.`%s` = %s.`%s`", 
			join.Table, join.Alias, baseAlias, join.LocalKey, join.Alias, join.ForeignKey))
	}

	var whereClauses []string
	var placeholders []interface{}

	if where != nil {
		for col, val := range where {
            // Simple type check assumption for array/slice
            // In real Go reflection is needed or specific type handling
			if vals, ok := val.([]interface{}); ok {
                qs := make([]string, len(vals))
                for i := range vals {
                    qs[i] = "?"
                    placeholders = append(placeholders, vals[i])
                }
                whereClauses = append(whereClauses, fmt.Sprintf("%s.`%s` IN (%s)", baseAlias, col, strings.Join(qs, ",")))
			} else {
				whereClauses = append(whereClauses, fmt.Sprintf("%s.`%s` = ?", baseAlias, col))
				placeholders = append(placeholders, val)
			}
		}
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	sql := fmt.Sprintf("SELECT %s FROM %s %s%s%s", 
		strings.Join(selectParts, ", "), baseTable, baseAlias, joinSQL.String(), whereSQL)

	return JoinQueryResult{
		SQL:          sql,
		Placeholders: placeholders,
		SelectParts:  selectParts,
	}
}
