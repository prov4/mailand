cmake_minimum_required(VERSION 3.24)
# The repo-wide version lives in ../VERSION (also used by the Go build and PKGBUILD).
file(STRINGS ${CMAKE_CURRENT_SOURCE_DIR}/../VERSION MAILAND_VERSION LIMIT_COUNT 1)
set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS ${CMAKE_CURRENT_SOURCE_DIR}/../VERSION)
project(mailand-ui VERSION ${MAILAND_VERSION} LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# WebEngine / WaylandClient are added in Milestone 3.
find_package(Qt6 6.5 REQUIRED COMPONENTS Core Gui Qml Quick QuickControls2 WebEngineCore WebEngineQuick)
qt_standard_project_setup(REQUIRES 6.5)
qt_policy(SET QTP0004 NEW)

find_package(Protobuf CONFIG REQUIRED)
find_package(gRPC CONFIG REQUIRED)

# --- protobuf / gRPC code generation from the shared contract ---------------
# protoc needs --proto_path to be an exact textual prefix of the file path, so normalize both.
set(PROTO_SRC_DIR ${CMAKE_CURRENT_SOURCE_DIR}/../proto)
cmake_path(NORMAL_PATH PROTO_SRC_DIR)
set(PROTO_FILE ${PROTO_SRC_DIR}/mailand.proto)
set(PROTO_DIR ${CMAKE_CURRENT_BINARY_DIR}/generated)
file(MAKE_DIRECTORY ${PROTO_DIR})
set(PROTO_SRCS ${PROTO_DIR}/mailand.pb.cc ${PROTO_DIR}/mailand.grpc.pb.cc)
set(PROTO_HDRS ${PROTO_DIR}/mailand.pb.h ${PROTO_DIR}/mailand.grpc.pb.h)

add_custom_command(
    OUTPUT ${PROTO_SRCS} ${PROTO_HDRS}
    COMMAND $<TARGET_FILE:protobuf::protoc>
            --proto_path=${PROTO_SRC_DIR}
            --cpp_out=${PROTO_DIR}
            --grpc_out=${PROTO_DIR}
            --plugin=protoc-gen-grpc=$<TARGET_FILE:gRPC::grpc_cpp_plugin>
            ${PROTO_FILE}
    DEPENDS ${PROTO_FILE}
    COMMENT "Generating C++ protobuf/gRPC sources"
    VERBATIM)

add_library(mailand_proto STATIC ${PROTO_SRCS} ${PROTO_HDRS})
target_include_directories(mailand_proto PUBLIC ${PROTO_DIR})
target_link_libraries(mailand_proto PUBLIC protobuf::libprotobuf gRPC::grpc++)
set_target_properties(mailand_proto PROPERTIES POSITION_INDEPENDENT_CODE ON)

# --- UI executable -----------------------------------------------------------
file(GLOB MAILAND_QML_FILES CONFIGURE_DEPENDS RELATIVE ${CMAKE_CURRENT_SOURCE_DIR} qml/*.qml)
if(EXISTS ${CMAKE_CURRENT_SOURCE_DIR}/qml/Theme.qml)
    set_source_files_properties(qml/Theme.qml PROPERTIES QT_QML_SINGLETON_TYPE TRUE)
endif()

qt_add_executable(mailand-ui src/main.cpp)

qt_add_qml_module(mailand-ui
    URI Mailand
    VERSION 1.0
    QML_FILES ${MAILAND_QML_FILES}
    SOURCES
        src/Rpc.h src/Rpc.cpp
        src/FolderModel.h src/FolderModel.cpp
        src/MessageModel.h src/MessageModel.cpp
        src/MessageView.h src/MessageView.cpp
        src/Composer.h src/Composer.cpp
        src/CalendarModel.h src/CalendarModel.cpp
        src/ContactsModel.h src/ContactsModel.cpp
        src/MailWeb.h src/MailWeb.cpp
        src/Format.h src/Format.cpp
        src/SelfTest.h src/SelfTest.cpp
)

# The generated QML type registration includes headers by bare name.
target_include_directories(mailand-ui PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_compile_options(mailand-ui PRIVATE -Wall -Wextra)
target_compile_definitions(mailand-ui PRIVATE MAILAND_VERSION="${MAILAND_VERSION}")

target_link_libraries(mailand-ui PRIVATE
    Qt6::Core Qt6::Gui Qt6::Qml Qt6::Quick Qt6::QuickControls2
    Qt6::WebEngineCore Qt6::WebEngineQuick
    mailand_proto)
