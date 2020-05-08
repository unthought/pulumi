// Copyright 2016-2020, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dotnet

import (
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2/model"
	"github.com/zclconf/go-cty/cty"
)

const (
	// intrinsicAwait is the name of the await intrinsic.
	intrinsicAwait = "__await"
	intrinsicConstructor = "__constructor"
)

// newAwaitCall creates a new call to the await intrinsic.
func newAwaitCall(promise model.Expression) model.Expression {
	// TODO(pdg): unions
	promiseType, ok := promise.Type().(*model.PromiseType)
	if !ok {
		return promise
	}

	return &model.FunctionCallExpression{
		Name: intrinsicAwait,
		Signature: model.StaticFunctionSignature{
			Parameters: []model.Parameter{{
				Name: "promise",
				Type: promiseType,
			}},
			ReturnType: promiseType.ElementType,
		},
		Args: []model.Expression{promise},
	}
}

// newAwaitCall creates a new call to the await intrinsic.
func newConstructorCall(obj model.Expression, name string) model.Expression {
	className := &model.LiteralValueExpression{Value: cty.StringVal(name)}
	return &model.FunctionCallExpression{
		Name: intrinsicConstructor,
		Signature: model.StaticFunctionSignature{
			Parameters: []model.Parameter{{
				Name: "boo",
				Type: model.StringType,
			}},
			ReturnType: obj.Type(),
		},
		Args: []model.Expression{className, obj},
	}
}
